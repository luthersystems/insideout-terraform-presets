// Default placeholder for the gcp_cloud_functions preset. Returns 200 OK so a
// fresh stack applies clean against Cloud Build's Node.js Buildpack. Override
// by setting var.source_archive_bucket and var.source_archive_object to your
// own pre-built bundle.
exports.helloWorld = (req, res) => {
  res.status(200).send('OK');
};
