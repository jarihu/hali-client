package networking

// PublishReachabilityPolicy models publishing intent independent of endpoint URLs.
type PublishReachabilityPolicy struct {
	RequiresInternetReachability bool
}

// PublishRequiresConfirmation returns true when the policy requires internet reachability.
// Since hali is LAN-only, unreachable publish confirmation is never required.
func PublishRequiresConfirmation(policy PublishReachabilityPolicy) bool {
	_ = policy
	return false
}
