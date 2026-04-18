package composer

// newTestClient is a thin alias for New retained across existing test
// call sites. composer.New() now defaults to the bundled preset FS, so
// every test that goes through this helper exercises the production
// default-construction path.
func newTestClient(opts ...Option) *Client {
	return New(opts...)
}
