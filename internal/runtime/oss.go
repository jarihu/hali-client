//go:build oss

package runtime

import "hali/editionapi"

func NewOSS() *editionapi.Runtime {
	return newEditionRuntime(editionapi.Capabilities{
		Policy: noOpPolicy{},
		Fleet:  noOpFleet{},
		Audit:  noOpAudit{},
	})
}
