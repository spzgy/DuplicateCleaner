//go:build windows

package backup

import (
	"fmt"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

// recycleToWindowsTrash 将单个文件移动到 Windows 回收站
func recycleToWindowsTrash(path string) error {
	const (
		FO_DELETE          = 3
		FOF_ALLOWUNDO      = 0x0040
		FOF_NOCONFIRMATION = 0x0010
		FOF_SILENT         = 0x0004
		FOF_NOERRORUI      = 0x0400
	)
	type shfileopstructW struct {
		hwnd                  uintptr
		wFunc                 uint32
		pFrom                 *uint16
		pTo                   *uint16
		fFlags                uint16
		fAnyOperationsAborted int32
		hNameMappings         uintptr
		lpszProgress          *uint16
	}
	p, err := toDoubleNullTerminatedUTF16(path)
	if err != nil {
		return err
	}
	op := shfileopstructW{
		wFunc:  FO_DELETE,
		pFrom:  p,
		fFlags: FOF_ALLOWUNDO | FOF_NOCONFIRMATION | FOF_SILENT | FOF_NOERRORUI,
	}
	shell32 := syscall.NewLazyDLL("shell32.dll")
	proc := shell32.NewProc("SHFileOperationW")
	r1, _, callErr := proc.Call(uintptr(unsafe.Pointer(&op)))
	if callErr != syscall.Errno(0) && callErr != nil {
		return callErr
	}
	if r1 != 0 {
		return syscall.Errno(r1)
	}
	if op.fAnyOperationsAborted != 0 {
		return fmt.Errorf("operation aborted")
	}
	return nil
}

// toDoubleNullTerminatedUTF16 将路径转换为双空终止 UTF-16
func toDoubleNullTerminatedUTF16(s string) (*uint16, error) {
	u := utf16.Encode([]rune(s))
	u = append(u, 0, 0)
	return &u[0], nil
}
