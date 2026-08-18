package foundation

import "list"

// #WorkloadStorageAllocationV1 is the provider-neutral, catalog-owned
// allocation fact for one workload component. Runtime modules may lower this
// fact into a named volume, bind mount, dataset, or another supported target,
// but the workload package must not invent storage lifecycle semantics.
#WorkloadStorageAllocationV1: #PersistentWorkloadStorageAllocationV1 | #CacheWorkloadStorageAllocationV1

#PersistentWorkloadStorageAllocationV1: close({
	componentRef: #ContractID
	volumeRef:    #ContractID
	target:       #AbsolutePath
	class:        "persistent"
	backup:       bool
	dataClasses: [...#DataClass] & list.MinItems(1)
	dataBindingRef: #ContractID

	_dataClassesUnique: list.UniqueItems(dataClasses) & true
})

#CacheWorkloadStorageAllocationV1: close({
	componentRef: #ContractID
	volumeRef:    #ContractID
	target:       #AbsolutePath
	class:        "cache"
	backup:       false
	dataClasses: []
	dataBindingRef?: _|_
})

// #WorkloadInfrastructureV1 is selected by every Application Kit alternative.
// The module identities are shared; only the declarative component allocations
// and workload data class binding vary by package.
#WorkloadInfrastructureV1: close({
	storageAllocation: {
		moduleRef: #ContractID
		allocations: [...#WorkloadStorageAllocationV1] & list.MinItems(1)

		_allocationIDsUnique: list.UniqueItems([
			for allocation in allocations {
				"\(allocation.componentRef)/\(allocation.volumeRef)"
			},
		]) & true
	}
	dataBinding: {
		moduleRef:  #ContractID
		bindingRef: #ContractID
		classes: [...#DataClass] & list.MinItems(1)
		locality: "primary-site" | "primary-or-replica"

		_classesUnique: list.UniqueItems(classes) & true
	}
	backupSource: {
		moduleRef: #ContractID
		allocations: [...close({
			componentRef: #ContractID
			volumeRef:    #ContractID
			dataClasses: [...#DataClass] & list.MinItems(1)
		})] & list.MinItems(1)
	}
	snapshot: moduleRef: #ContractID
	restore: moduleRef:  #ContractID
	recovery: moduleRef: #ContractID

	_storageAllocationRoleDistinct:     storageAllocation.moduleRef != dataBinding.moduleRef
	_storageAllocationBackupDistinct:   storageAllocation.moduleRef != backupSource.moduleRef
	_storageAllocationSnapshotDistinct: storageAllocation.moduleRef != snapshot.moduleRef
	_storageAllocationRestoreDistinct:  storageAllocation.moduleRef != restore.moduleRef
	_storageAllocationRecoveryDistinct: storageAllocation.moduleRef != recovery.moduleRef
	_dataBindingBackupDistinct:         dataBinding.moduleRef != backupSource.moduleRef
	_dataBindingSnapshotDistinct:       dataBinding.moduleRef != snapshot.moduleRef
	_dataBindingRestoreDistinct:        dataBinding.moduleRef != restore.moduleRef
	_dataBindingRecoveryDistinct:       dataBinding.moduleRef != recovery.moduleRef
	_backupSnapshotDistinct:            backupSource.moduleRef != snapshot.moduleRef
	_backupRestoreDistinct:             backupSource.moduleRef != restore.moduleRef
	_backupRecoveryDistinct:            backupSource.moduleRef != recovery.moduleRef
	_snapshotRestoreDistinct:           snapshot.moduleRef != restore.moduleRef
	_snapshotRecoveryDistinct:          snapshot.moduleRef != recovery.moduleRef
	_restoreRecoveryDistinct:           restore.moduleRef != recovery.moduleRef
	_persistentBindingsExact: [
		for allocation in storageAllocation.allocations
		if allocation.class == "persistent" {
			bindingRef: allocation.dataBindingRef & dataBinding.bindingRef
			classes: [for allocationClass in allocation.dataClasses {
				matches: [for bindingClass in dataBinding.classes if allocationClass == bindingClass {bindingClass}] & list.MinItems(1) & list.MaxItems(1)
			}]
		},
	]
	_backupAllocationCountExact: len(backupSource.allocations) & len([
		for allocation in storageAllocation.allocations
		if allocation.backup {allocation},
	])
	_backupAllocationsExact: [
		for source in backupSource.allocations {
			matches: [
				for allocation in storageAllocation.allocations
				if allocation.backup &&
					allocation.class == "persistent" &&
					allocation.componentRef == source.componentRef &&
					allocation.volumeRef == source.volumeRef {
					componentRef: allocation.componentRef
					volumeRef:    allocation.volumeRef
					classCount:   len(source.dataClasses) & len(allocation.dataClasses)
					classes: [
						for sourceClass in source.dataClasses {
							matches: [
								for allocationClass in allocation.dataClasses
								if sourceClass == allocationClass {allocationClass},
							] & list.MinItems(1) & list.MaxItems(1)
						},
					]
				},
			] & list.MinItems(1) & list.MaxItems(1)
		},
	]
})

// These contracts are deliberately small, deep seams. They carry plan
// authority only; provider lifecycle, storage credentials, and multi-server
// orchestration remain outside StackKits.
#StorageAllocationModuleContractV1: close({
	apiVersion:            "stackkit.storage-allocation/v1"
	kind:                  "StorageAllocationContract"
	authority:             "resolved-plan"
	rootSourceRef:         "storage.dataRoot"
	volumeDriverSourceRef: "storage.volumeDriver"
	allocationIdentity:    "workload/component/volume"
	supportedClasses: ["persistent", "cache"]
	providerLifecycleOwned:        false
	credentialCustodyOwned:        false
	multiServerOrchestrationOwned: false
})

#WorkloadDataBindingModuleContractV1: close({
	apiVersion:                    "stackkit.workload-data-binding/v1"
	kind:                          "WorkloadDataBindingContract"
	authority:                     "resolved-plan"
	sourceRef:                     "data.bindings"
	bindingIdentity:               "workload/service"
	classValidation:               "workload-subset"
	placementValidation:           "primary-or-declared-replica"
	storageAllocationModuleRef:    #ContractID
	providerLifecycleOwned:        false
	credentialCustodyOwned:        false
	multiServerOrchestrationOwned: false
})
