package editionapi

type Capabilities struct {
	Policy PolicyProvider
	Fleet  FleetProvider
	Audit  AuditProvider
}

type Runtime struct {
	Cap Capabilities
}
