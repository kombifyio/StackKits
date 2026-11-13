package decisionreceipt

// The ledger's catalogue operation ids for the first slice of operations whose
// confirmation a decision receipt carries.
//
// The ledger accepts a lowercase id with underscores, so these are not the
// dotted StackKits catalogue ids of the operations themselves: the ledger's id
// names the operation, the StackKits catalogue id names the command that
// implements it. The two are bound in FirstSlice.
const (
	// OperationBackupRestore activates one verified staged restore over the
	// live volumes.
	OperationBackupRestore = "backup_restore"
	// OperationApplicationRemove removes one application with its data.
	OperationApplicationRemove = "application_remove"
	// OperationAdvancedChangeSetApply applies one Owner-signed Advanced
	// change set.
	OperationAdvancedChangeSetApply = "advanced_change_set_apply"
	// OperationInstallationRemove removes the installation with its data.
	OperationInstallationRemove = "installation_remove"
)

// Operation is one first-slice operation: the ledger's id, the StackKits
// catalogue id of the command that implements it, and the MCP tool that
// reaches it (empty when no MCP tool does).
type Operation struct {
	// LedgerID is the operation id the ledger signs in the intent.
	LedgerID string
	// CatalogueID is the StackKits catalogue id of the operation.
	CatalogueID string
	// MCPTool is the MCP tool that reaches the operation.
	MCPTool string
}

// FirstSlice is the first slice of operations whose confirmation a signed
// decision receipt carries instead of a self-asserted boolean.
func FirstSlice() []Operation {
	return []Operation{
		{LedgerID: OperationBackupRestore, CatalogueID: "stackkit.backup.restore.activate", MCPTool: "stackkit_backup_restore_activate"},
		{LedgerID: OperationApplicationRemove, CatalogueID: "stackkit.remove", MCPTool: "stackkit_remove"},
		{LedgerID: OperationApplicationRemove, CatalogueID: "stackkit.app.own", MCPTool: "stackkit_own_application"},
		{LedgerID: OperationAdvancedChangeSetApply, CatalogueID: "stackkit.advanced.change-set.apply", MCPTool: "stackkit_advanced_change_set_apply"},
		{LedgerID: OperationInstallationRemove, CatalogueID: "stackkit.remove"},
	}
}

// IsFirstSlice reports whether a ledger operation id is in the first slice.
func IsFirstSlice(ledgerID string) bool {
	for _, operation := range FirstSlice() {
		if operation.LedgerID == ledgerID {
			return true
		}
	}
	return false
}

// The canonical argument objects (docs/DECISION-RECEIPTS.md, "Binding
// definition"). Every member is always present, so the producer of a decision
// and the consumer of its receipt build exactly the same object; an unused
// string member is the empty string. The member names are the contract
// between this repository and the producer: the ledger never sees the
// arguments themselves, only their digest (ArgumentsDigest).

// BackupRestoreArguments is the canonical argument object of one restore
// activation.
func BackupRestoreArguments(resultID, workload, marketplaceService, operationID string) map[string]any {
	return map[string]any{
		"restore_result_id":   resultID,
		"workload":            workload,
		"marketplace_service": marketplaceService,
		"operation_id":        operationID,
	}
}

// ApplicationRemoveArguments is the canonical argument object of removing one
// application workload with its data.
func ApplicationRemoveArguments(workload string, deleteData bool) map[string]any {
	return map[string]any{
		"workload":    workload,
		"delete_data": deleteData,
	}
}

// OwnApplicationRemoveArguments is the canonical argument object of removing
// one own application with its data.
func OwnApplicationRemoveArguments(name string, deleteData, keepSecrets bool, operationID string) map[string]any {
	return map[string]any{
		"name":         name,
		"delete_data":  deleteData,
		"keep_secrets": keepSecrets,
		"operation_id": operationID,
	}
}

// InstallationRemoveArguments is the canonical argument object of removing
// the installation with its data.
func InstallationRemoveArguments(deleteData, purge bool) map[string]any {
	return map[string]any{
		"delete_data": deleteData,
		"purge":       purge,
	}
}

// AdvancedChangeSetApplyArguments is the canonical argument object of
// applying one Advanced change set. The digest of the stored change-set bytes
// is the operation's plan digest (planSha256), not an argument; the candidate
// and capability paths are host local and never enter a digest, because the
// Owner-signed change set those bytes hold already binds what they name.
func AdvancedChangeSetApplyArguments(changeSetID string) map[string]any {
	return map[string]any{
		"change_set_id": changeSetID,
	}
}
