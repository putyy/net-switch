//go:build darwin && cgo

package darwin

/*
#cgo LDFLAGS: -framework CoreLocation -framework CoreWLAN -framework Cocoa -framework Foundation
#include <stdlib.h>
#include "wifi.h"
*/
import "C"

import (
	"fmt"
	"unsafe"
)

type nativeSSIDProvider struct{}

func (nativeSSIDProvider) CurrentSSID(interfaceName string) (string, ssidAccess, error) {
	name := C.CString(interfaceName)
	defer C.free(unsafe.Pointer(name))

	buffer := make([]byte, 1024)
	status := C.NetSwitchCopyCurrentSSID(name, (*C.char)(unsafe.Pointer(&buffer[0])), C.size_t(len(buffer)))
	switch status {
	case C.NET_SWITCH_SSID_AVAILABLE:
		return C.GoString((*C.char)(unsafe.Pointer(&buffer[0]))), ssidAccessAvailable, nil
	case C.NET_SWITCH_SSID_PENDING:
		return "", ssidAccessPending, nil
	case C.NET_SWITCH_SSID_DENIED:
		return "", ssidAccessDenied, nil
	case C.NET_SWITCH_SSID_RESTRICTED:
		return "", ssidAccessRestricted, nil
	case C.NET_SWITCH_SSID_UNAVAILABLE:
		return "", ssidAccessUnavailable, nil
	default:
		return "", ssidAccessUnavailable, fmt.Errorf("CoreWLAN returned unknown status %d", int(status))
	}
}
