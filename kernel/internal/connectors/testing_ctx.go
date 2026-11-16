package connectors

import "context"

type testingKey struct{}

// WithTesting marks a context as the owner's connector test run (contract 0033 §3): the one call that may run while
// the connector is still untested. Only the test endpoint sets it.
func WithTesting(ctx context.Context) context.Context {
	return context.WithValue(ctx, testingKey{}, true)
}

func isTesting(ctx context.Context) bool {
	v, _ := ctx.Value(testingKey{}).(bool)
	return v
}
