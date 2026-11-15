package engine

import (
	"reflect"
	"testing"
)

// The CEL device object is {id, code, outlet} (contract 0030 §4, 0058 §2); empty without a paired device.
func TestDeviceVar(t *testing.T) {
	a := Actor{Org: "o", ID: "kasir@x.id", Device: &DeviceRef{ID: "dev_0123456789abcdef0123", Code: "K2", Outlet: "OUT-01"}}
	want := map[string]any{"id": "dev_0123456789abcdef0123", "code": "K2", "outlet": "OUT-01"}
	if got, ok := GlobalVar(nil, a, "device"); !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("device = %v %v", got, ok)
	}
	if got := DeviceVar(Actor{}); !reflect.DeepEqual(got, map[string]any{"id": "", "code": "", "outlet": ""}) {
		t.Fatalf("no device = %v", got)
	}
}
