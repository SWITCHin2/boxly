package vm

// Label and annotation keys applied to every managed Kubernetes object.
// VM state is reconstructed from these, so there is no separate datastore.
const (
	LabelManaged  = "ongo.dev/managed"  // "true" on everything we own
	LabelVMID     = "ongo.dev/vm-id"    // short id, also the resource name suffix
	LabelType     = "ongo.dev/type"     // sandbox | persistent
	LabelOwner    = "ongo.dev/owner"    // token subject; "default" in the MVP
	LabelPool     = "ongo.dev/pool"     // warm | claimed (sandbox only)
	LabelTemplate = "ongo.dev/template" // template id the box was created from
	LabelReady    = "ongo.dev/ready"    // "true" once a warm box's setup is applied

	AnnoExpiresAt = "ongo.dev/expires-at" // RFC3339 expiry for TTL janitor
	AnnoName      = "ongo.dev/name"       // human-friendly name

	PoolWarm    = "warm"
	PoolClaimed = "claimed"

	// ServiceAccount the VM pods run as. Created by deploy/00-namespace-rbac.yaml.
	vmServiceAccount = "ongo-vm"
)
