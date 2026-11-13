//go:build !linux && !darwin

package cairn

func storageDiskSpace(string) (free, total int64, ok bool) {
	return 0, 0, false
}
