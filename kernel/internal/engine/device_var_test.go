package engine

import (
	"context"
	"reflect"
	"testing"
)

// The CEL device object is {id, code, outlet, event} (contract 0030 §4, 0058 §2): empty without a paired device; event
// is true only while the unit of work is a device event write (contract 0058 §4), not for a live request from a paired
// browser.
func TestDeviceVar(t *testing.T) {
	a := Actor{Org: "o", ID: "kasir@x.id", Device: &DeviceRef{ID: "dev_0123456789abcdef0123", Code: "K2", Outlet: "OUT-01"}}
	want := map[string]any{"id": "dev_0123456789abcdef0123", "code": "K2", "outlet": "OUT-01", "event": false}
	if got, ok := GlobalVar(nil, a, "device"); !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("device = %v %v", got, ok)
	}
	if got := DeviceVar(Actor{}); !reflect.DeepEqual(got, map[string]any{"id": "", "code": "", "outlet": "", "event": false}) {
		t.Fatalf("no device = %v", got)
	}
	if got := deviceVarIn(context.Background(), a)["event"]; got != false {
		t.Fatalf("live request from a paired device: event = %v", got)
	}
	ev := WithDeviceWrite(context.Background(), DeviceWrite{Model: "pos_sale"})
	if got := deviceVarIn(ev, a)["event"]; got != true {
		t.Fatalf("device event write: event = %v", got)
	}
	if got := deviceVarIn(ev, Actor{})["event"]; got != false {
		t.Fatalf("event write without a device: event = %v", got)
	}
}
