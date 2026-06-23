package vm

// Label and annotation keys applied to every managed Kubernetes object.
// VM state is reconstructed from these, so there is no separate datastore.
const (
	LabelManaged  = "boxly.dev/managed"  // "true" on everything we own
	LabelVMID     = "boxly.dev/vm-id"    // short id, also the resource name suffix
	LabelType     = "boxly.dev/type"     // sandbox | persistent
	LabelOwner    = "boxly.dev/owner"    // token subject; "default" in the MVP
	LabelPool     = "boxly.dev/pool"     // warm | claimed (sandbox only)
	LabelTemplate = "boxly.dev/template" // template id the box was created from
	LabelReady    = "boxly.dev/ready"    // "true" once a warm box's setup is applied

	AnnoExpiresAt = "boxly.dev/expires-at" // RFC3339 expiry for TTL janitor
	AnnoName      = "boxly.dev/name"       // human-friendly name

	PoolWarm    = "warm"
	PoolClaimed = "claimed"

	// ServiceAccount the VM pods run as. Created by deploy/00-namespace-rbac.yaml.
	vmServiceAccount = "boxly-vm"
)
