package networking

// Capabilities are transport-level flags resolved from semantic mode intent.
type Capabilities struct {
	EnableLSD bool
}

func ResolveCapabilities(_ Mode) Capabilities {
	return Capabilities{EnableLSD: true}
}
