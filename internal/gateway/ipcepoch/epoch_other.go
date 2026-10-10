//go:build (!linux && !darwin) || (darwin && !arm64) || (linux && !arm64 && !amd64)

package ipcepoch

func Current() (Epoch, error) { return Epoch{}, ErrUnsupported }
