// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1

// IngressApplyConfiguration represents an declarative configuration of the Ingress type for use
// with apply.
type IngressApplyConfiguration struct {
	ConsoleURL         *string `json:"consoleURL,omitempty"`
	ClientDownloadsURL *string `json:"clientDownloadsURL,omitempty"`
}

// IngressApplyConfiguration constructs an declarative configuration of the Ingress type for use with
// apply.
func Ingress() *IngressApplyConfiguration {
	return &IngressApplyConfiguration{}
}

// WithConsoleURL sets the ConsoleURL field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ConsoleURL field is set to the value of the last call.
func (b *IngressApplyConfiguration) WithConsoleURL(value string) *IngressApplyConfiguration {
	b.ConsoleURL = &value
	return b
}

// WithClientDownloadsURL sets the ClientDownloadsURL field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ClientDownloadsURL field is set to the value of the last call.
func (b *IngressApplyConfiguration) WithClientDownloadsURL(value string) *IngressApplyConfiguration {
	b.ClientDownloadsURL = &value
	return b
}
