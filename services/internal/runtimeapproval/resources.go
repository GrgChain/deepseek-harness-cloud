package runtimeapproval

const GiB int64 = 1 << 30
const MaxStorageGiB int64 = 100

// SupportedStorage bounds new hard-capacity filesystems. Defaults never resize
// existing assets, which keep their frozen capacity and identity.
func SupportedStorage(storage int64) bool {
	return storage >= GiB && storage <= MaxStorageGiB*GiB && storage%GiB == 0
}

// SupportedResources bounds the local executor's reviewed resource choices.
func SupportedResources(cpu, memory, storage int64) bool {
	return (cpu == 1000 || cpu == 2000 || cpu == 4000 || cpu == 8000 || cpu == 16000) && (memory == 2<<30 || memory == 4<<30 || memory == 8<<30 || memory == 16<<30 || memory == 32<<30 || memory == 64<<30) && SupportedStorage(storage)
}
