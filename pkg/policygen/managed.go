package policygen

// managedPolicyServices maps each AWS managed policy the presets (and the
// pinned registry modules) attach to a role, to the services that policy
// grants. The workload boundary must allow these services, or a role that
// works today (an EKS node pulling from ECR, the SSM agent on a bastion)
// would lose them once the boundary is attached.
//
// Why a table: a boundary only caps a role, so a managed policy that grants
// a service the boundary omits silently loses that service. The generator
// can not read the managed policy bodies offline, so each one the presets
// attach must have a row here, and TestEveryManagedPolicyMapped fails when a
// preset attaches a new one (#935). The service lists come from the AWS
// managed policy reference; the Phase 1 CloudTrail shadow run checks them.
var managedPolicyServices = map[string][]string{
	"AWSXRayDaemonWriteAccess":                           {"xray"},
	"AmazonEC2ContainerRegistryPullOnly":                 {"ecr"},
	"AmazonEC2ContainerRegistryReadOnly":                 {"ecr"},
	"AmazonEKSFargatePodExecutionRolePolicy":             {"ecr"},
	"AmazonEKSWorkerNodePolicy":                          {"ec2", "eks", "eks-auth"},
	"AmazonEKSWorkerNodeMinimalPolicy":                   {"ec2", "eks-auth"},
	"AmazonEKS_CNI_Policy":                               {"ec2"},
	"AmazonEKSClusterPolicy":                             {"autoscaling", "ec2", "elasticloadbalancing", "kms"},
	"AmazonEKSLocalOutpostClusterPolicy":                 {"ec2", "ssm", "ssmmessages", "ec2messages", "secretsmanager", "logs"},
	"AmazonEKSComputePolicy":                             {"ec2", "ssm"},
	"AmazonEKSBlockStoragePolicy":                        {"ec2", "kms"},
	"AmazonEKSLoadBalancingPolicy":                       {"ec2", "elasticloadbalancing", "shield", "wafv2", "waf-regional", "acm", "cognito-idp"},
	"AmazonEKSNetworkingPolicy":                          {"ec2"},
	"AmazonSSMManagedInstanceCore":                       {"ssm", "ssmmessages", "ec2messages"},
	"AmazonSageMakerFullAccess":                          {"sagemaker", "ecr", "ec2", "s3", "logs", "cloudwatch", "kms", "sns", "application-autoscaling", "elasticfilesystem", "fsx", "glue", "athena", "secretsmanager", "servicecatalog", "codecommit", "elastic-inference", "redshift", "redshift-data", "lambda", "events"},
	"CloudWatchAgentServerPolicy":                        {"cloudwatch", "logs", "ec2", "ssm"},
	"service-role/AWSAppRunnerServicePolicyForECRAccess": {"ecr"},
	"service-role/AWSBackupServiceRolePolicyForBackup":   {"backup", "ec2", "rds", "dynamodb", "elasticfilesystem", "fsx", "storagegateway", "s3", "kms", "tag", "redshift", "timestream", "cloudformation", "ssm"},
	"service-role/AWSBackupServiceRolePolicyForRestores": {"backup", "ec2", "rds", "dynamodb", "elasticfilesystem", "fsx", "storagegateway", "s3", "kms", "redshift", "timestream", "cloudformation", "ssm"},
	"service-role/AWSLambdaBasicExecutionRole":           {"logs"},
	"service-role/AWSLambdaVPCAccessExecutionRole":       {"logs", "ec2"},
	"service-role/AmazonEBSCSIDriverPolicy":              {"ec2"},
	"service-role/AmazonRDSEnhancedMonitoringRole":       {"logs"},
}
