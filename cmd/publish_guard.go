package cmd

import (
	"hali/internal/networking"
)

var allowUnreachablePublish bool

func shouldAllowUnreachablePublish(policy networking.PublishReachabilityPolicy, publishKind string) (bool, error) {
	_ = publishKind
	return networking.PublishRequiresConfirmation(policy) && allowUnreachablePublish, nil
}
