mock_provider "aws" {}

# Issue #758. Pins the OpenSearch Serverless (AOSS) VECTORSEARCH collection
# as the production vector-store path of the AI stack.
#
# What this locks down, end to end:
#   - the collection is created with type = "VECTORSEARCH" (not SEARCH or
#     TIMESERIES) — Bedrock Knowledge Bases and the app's k-NN index require
#     the vector engine; a silent flip to another collection type would let
#     index creation fail only at apply time.
#   - the AOSS security-policy TRIO required for collection-create is shaped
#     correctly: encryption + network emitted HERE, data-access deliberately
#     NOT emitted here (it lives in aws/bedrock — see main.tf header).
#   - the serverless-only OCU alarms (account-level AWS/AOSS metrics) exist
#     and stay absent in managed mode.

run "vectorsearch_collection" {
  command = plan

  # Force the AOSS SLR probe to "role present" so the test exercises the
  # steady-state path (no SLR create race) — orthogonal to what we assert.
  override_data {
    target = data.aws_iam_roles.aoss_slr[0]
    values = {
      names = ["AWSServiceRoleForAmazonOpenSearchServerless"]
    }
  }

  variables {
    project         = "vec-test"
    region          = "us-east-1"
    environment     = "test"
    deployment_type = "serverless"
  }

  # --- Collection is created, exactly one, and is the VECTORSEARCH engine.
  assert {
    condition     = length(aws_opensearchserverless_collection.serverless) == 1
    error_message = "Serverless mode must create exactly one AOSS collection."
  }

  assert {
    condition     = aws_opensearchserverless_collection.serverless[0].type == "VECTORSEARCH"
    error_message = "AOSS collection must be type VECTORSEARCH — Bedrock KB + the app k-NN index require the vector engine; SEARCH/TIMESERIES would fail index creation at apply time."
  }

  assert {
    condition     = aws_opensearchserverless_collection.serverless[0].name == "vec-test-search"
    error_message = "AOSS collection name must be <project>-search — the encryption + network policies and the bedrock data-access policy all match the collection by this exact name."
  }

  # --- Policy TRIO shape. AOSS collection-create fails unless encryption +
  # network exist; data-access is the consumer's job (aws/bedrock).
  assert {
    condition     = length(aws_opensearchserverless_security_policy.encryption) == 1
    error_message = "Exactly one AOSS encryption policy must exist — collection-create fails without it."
  }

  assert {
    condition     = aws_opensearchserverless_security_policy.encryption[0].type == "encryption"
    error_message = "The encryption security policy must declare type = encryption."
  }

  assert {
    condition     = length(aws_opensearchserverless_security_policy.network) == 1
    error_message = "Exactly one AOSS network policy must exist — collection-create fails without it."
  }

  assert {
    condition     = aws_opensearchserverless_security_policy.network[0].type == "network"
    error_message = "The network security policy must declare type = network."
  }

  # --- Both security policies scope to the SAME collection name the
  # collection is created under. A drift here (policy targets a different
  # collection) is the classic AOSS "AccessDeniedException on collection
  # create" and is invisible without an explicit assertion.
  assert {
    condition     = contains(jsondecode(aws_opensearchserverless_security_policy.encryption[0].policy).Rules[0].Resource, "collection/vec-test-search")
    error_message = "Encryption policy Rules must scope to collection/<name> matching the created collection."
  }

  assert {
    condition     = contains(jsondecode(aws_opensearchserverless_security_policy.network[0].policy)[0].Rules[0].Resource, "collection/vec-test-search")
    error_message = "Network policy Rules must scope to collection/<name> matching the created collection."
  }

  # --- Data-access policy is NOT emitted by this module. The trio's third
  # leg lives in aws/bedrock (keyed by collection NAME). This preset declares
  # no aws_opensearchserverless_access_policy resource at all, so its absence
  # is structurally guaranteed: adding one here would make this preset emit a
  # data-access policy that duplicates / conflicts with bedrock's. We cannot
  # assert "no resource of type X" in tftest (you can only reference declared
  # resources), so the guard is the main.tf header comment + the bedrock-side
  # ownership; this is documented, not asserted.

  # --- Serverless OCU alarms exist (account-level AWS/AOSS metrics).
  assert {
    condition     = length(aws_cloudwatch_metric_alarm.search_ocu) == 1
    error_message = "Serverless mode must create the AOSS SearchOCU alarm so runaway search-OCU scale-out pages instead of surfacing on the bill."
  }

  assert {
    condition     = aws_cloudwatch_metric_alarm.search_ocu["0"].namespace == "AWS/AOSS"
    error_message = "SearchOCU alarm must watch the AWS/AOSS namespace."
  }

  assert {
    condition     = aws_cloudwatch_metric_alarm.search_ocu["0"].metric_name == "SearchOCU"
    error_message = "SearchOCU alarm must watch the SearchOCU metric."
  }

  assert {
    condition     = contains(keys(aws_cloudwatch_metric_alarm.search_ocu["0"].dimensions), "ClientId")
    error_message = "AOSS OCU metrics are account-level — the alarm must use the ClientId dimension, not a per-collection dimension (no per-collection OCU metric exists)."
  }

  assert {
    condition     = length(aws_cloudwatch_metric_alarm.indexing_ocu) == 1
    error_message = "Serverless mode must create the AOSS IndexingOCU alarm."
  }

  assert {
    condition     = aws_cloudwatch_metric_alarm.indexing_ocu["0"].metric_name == "IndexingOCU"
    error_message = "IndexingOCU alarm must watch the IndexingOCU metric."
  }

  # --- The cluster_red (managed AWS/ES) alarm must NOT exist in serverless
  # mode — AOSS publishes no ClusterStatus metric.
  assert {
    condition     = length(aws_cloudwatch_metric_alarm.cluster_red) == 0
    error_message = "Managed-domain cluster_red alarm must not exist in serverless mode — AOSS has no ClusterStatus.red metric."
  }
}

run "serverless_collection_outputs" {
  # apply (not plan): the AOSS collection's arn / collection_endpoint / id are
  # apply-time identifiers, unknown during plan even under mock_provider. The
  # mock provider synthesises concrete values on apply so the non-null
  # assertions below can evaluate. (mock_provider performs no real API calls.)
  command = apply

  override_data {
    target = data.aws_iam_roles.aoss_slr[0]
    values = {
      names = ["AWSServiceRoleForAmazonOpenSearchServerless"]
    }
  }

  variables {
    project         = "vec-test"
    region          = "us-east-1"
    environment     = "test"
    deployment_type = "serverless"
  }

  # collection_arn / collection_name are consumed by aws/bedrock wiring;
  # collection_endpoint / collection_id are for the app/ingestion tier that
  # creates the vector index. All four must be non-null in serverless mode.
  assert {
    condition     = output.collection_arn != null
    error_message = "collection_arn must be non-null in serverless mode — aws/bedrock.opensearch_collection_arn wires from it."
  }

  assert {
    condition     = output.collection_name != null
    error_message = "collection_name must be non-null in serverless mode — the bedrock data-access policy matches the collection by name."
  }

  assert {
    condition     = output.collection_endpoint != null
    error_message = "collection_endpoint must be non-null in serverless mode — the app targets it to create the vector index and run k-NN queries."
  }

  assert {
    condition     = output.collection_id != null
    error_message = "collection_id must be non-null in serverless mode."
  }
}

run "managed_mode_serverless_collection_outputs_null" {
  command = plan

  override_data {
    target = data.aws_iam_roles.opensearch_slr[0]
    values = {
      names = ["AWSServiceRoleForAmazonOpenSearchService"]
    }
  }

  variables {
    project         = "vec-test"
    region          = "us-east-1"
    environment     = "test"
    deployment_type = "managed"
    vpc_id          = "vpc-12345"
    subnet_ids      = ["subnet-aaa"]
  }

  # In managed mode there is no AOSS collection — every serverless-only
  # output must be null so a consumer that mis-wires managed-mode opensearch
  # into bedrock gets a clean null, not a dangling [0] index error.
  assert {
    condition     = output.collection_arn == null
    error_message = "collection_arn must be null in managed mode."
  }

  assert {
    condition     = output.collection_endpoint == null
    error_message = "collection_endpoint must be null in managed mode."
  }

  assert {
    condition     = output.collection_id == null
    error_message = "collection_id must be null in managed mode."
  }

  # No serverless OCU alarms and no AOSS collection in managed mode.
  assert {
    condition     = length(aws_cloudwatch_metric_alarm.search_ocu) == 0
    error_message = "SearchOCU alarm must not exist in managed mode — AOSS metrics are not published for OpenSearch Service domains."
  }

  assert {
    condition     = length(aws_cloudwatch_metric_alarm.indexing_ocu) == 0
    error_message = "IndexingOCU alarm must not exist in managed mode."
  }

  assert {
    condition     = length(aws_opensearchserverless_collection.serverless) == 0
    error_message = "No AOSS collection in managed mode."
  }
}
