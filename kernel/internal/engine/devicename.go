package engine

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Names made on the device (contract 0058 §3).

// DeviceWrite is a write that came from a paired device: the model of its own document ("" = the model the
// Engine.Create or Engine.Submit call names), the name the device made for it (used when that model has naming_on:
// device), when it happened on the device, the money it charged and the event it came with. Commit, when set, runs
// inside each unit of work's transaction just before it commits, with the unit's target (<model>/<name> of the
// document it submitted or cancelled, else the first record it wrote): the edge event is stored with the write
// (contract 0058 §4), so a replay never writes twice; an error rolls the write back.
type DeviceWrite struct {
	Model      string
	Name       string
	OccurredAt time.Time
	Money      map[string]any
	Event      string
	Commit     func(ctx context.Context, q pg.Querier, target string) error
}

type deviceWriteKey struct{}

// WithDeviceWrite marks the units of work run under ctx as dw. One such ctx is meant for one unit of work (one
// Engine call): dw.Commit fires in every unit run under it.
func WithDeviceWrite(ctx context.Context, dw DeviceWrite) context.Context {
	return context.WithValue(ctx, deviceWriteKey{}, &dw)
}

// deviceWriteOf is the device write ctx carries, or nil.
func deviceWriteOf(ctx context.Context) *DeviceWrite {
	if ctx == nil {
		return nil
	}
	dw, _ := ctx.Value(deviceWriteKey{}).(*DeviceWrite)
	return dw
}

// deviceWriteFor is the device write of ctx when its own document is of model, else nil.
func deviceWriteFor(ctx context.Context, model string) *DeviceWrite {
	if dw := deviceWriteOf(ctx); dw != nil && (dw.Model == "" || dw.Model == model) {
		return dw
	}
	return nil
}

// docName names a new m record: with the device's name when it is the device write's own document (own) of a
// naming_on: device model, else by the kernel (nextName). Records a flow or hook makes in the same unit are never
// own, so they are numbered by the kernel.
func (o *op) docName(m *meta.Model, rec store.Record, own bool) (string, error) {
	if own && m.NamingOn == "device" {
		if dw := deviceWriteFor(o.ctx, m.Name); dw != nil {
			return o.deviceName(m, rec, dw)
		}
	}
	return o.nextName(m, rec)
}

// devicePattern is the string pattern device names of m follow in org: the scheme the kernel resolves (a
// config.numbering override included, contract 0009 §3), so head office and devices share it; the model's own
// naming when that scheme is a series or has no {device.code}.
func (e *Engine) devicePattern(m *meta.Model, org string) string {
	if s := e.scheme(m, org); s.Series == "" && strings.Contains(s.Pattern, deviceCodeToken) {
		return s.Pattern
	}
	return m.Naming
}

// deviceCodeToken is the token of the paired device's code; validation makes it followed by "-".
const deviceCodeToken = "{device.code}"

// withoutDeviceCode is pattern without {device.code}: the kernel's own numbering of a naming_on: device model for
// a write that comes without a device (head office), e.g. STRK-{outlet.code}-{YY}{MM}-{#####}.
func withoutDeviceCode(pattern string) string {
	return strings.ReplaceAll(pattern, deviceCodeToken+"-", "")
}

func deviceNameInvalid(msg string) error {
	return &StatusError{Status: 422, Code: "device_name_invalid", Msg: msg}
}

// deviceName is the name of a new m record that the device write dw brings, checked and counted: the moment the
// kernel would make the name (create of a non-submittable, submit of a submittable). The record's outlet link, when
// m has one, must be the device's outlet. An existing name is 409 duplicate_name; a number past the device's last
// number + 1 in that period is accepted and leaves a device_number_gap exception for head office.
func (o *op) deviceName(m *meta.Model, rec store.Record, dw *DeviceWrite) (string, error) {
	e, a := o.e, o.actor
	if a.Device == nil {
		return "", deviceNameInvalid("Nomor dokumen dari perangkat hanya diterima dari perangkat yang sudah dipasangkan")
	}
	if dw.Name == "" {
		return "", deviceNameInvalid("Perangkat harus mengirim nomor " + strings.ToLower(m.Label.Text()) + " bersama datanya")
	}
	if f := m.Field("outlet"); f != nil && f.Type == "link" && f.To == "outlet" && str(rec["outlet"]) != a.Device.Outlet {
		return "", deviceNameInvalid("Outlet dokumen harus outlet perangkat ini (" + a.Device.Outlet + ")")
	}
	at := dw.OccurredAt
	if at.IsZero() {
		at = e.Now()
	}
	at = at.In(e.Location())
	n, err := e.checkDeviceName(o.ctx, o.tx, m, a, dw.Name, at)
	if err != nil {
		return "", err
	}
	series := "device:" + a.Device.ID + ":" + m.Name
	prev, err := store.ClaimDeviceNumber(o.ctx, o.tx, a.Org, series, store.Period(store.DefaultReset(e.devicePattern(m, a.Org)), at), n)
	if err != nil {
		return "", err
	}
	// after the claim: a concurrent write of the same name by this device has committed or rolled back by now
	taken, err := store.Exists(o.ctx, o.tx, a.Org, m.Name, dw.Name)
	if err != nil {
		return "", err
	}
	if taken {
		return "", conflict("duplicate_name", "Nomor "+dw.Name+" sudah dipakai")
	}
	if n > prev+1 {
		if err := store.RecordException(o.ctx, o.tx, store.Exception{Org: a.Org, Outlet: a.Device.Outlet, Device: a.Device.ID,
			Kind: "device_number_gap", Ref: m.Name + "/" + dw.Name, Detail: map[string]any{"expected": prev + 1, "got": n}}); err != nil {
			return "", err
		}
	}
	return dw.Name, nil
}

// digitsRe is the number part of a device name.
var digitsRe = regexp.MustCompile(`^[0-9]{1,18}$`)

// checkDeviceName checks that name is m's device pattern (devicePattern) rendered for device a at time at (already in the site timezone):
// {device.code} is the device's code, {outlet.<field>} that field of the device's outlet record, the date tokens
// those of at, and the counter any number from 1, written as the kernel writes it (zero-padded to the counter's
// width). It returns the number. Any other field token cannot be rendered for a device and refuses the name.
func (e *Engine) checkDeviceName(ctx context.Context, q pg.Querier, m *meta.Model, a Actor, name string, at time.Time) (int64, error) {
	if a.Device == nil {
		return 0, deviceNameInvalid("Nomor dokumen dari perangkat hanya diterima dari perangkat yang sudah dipasangkan")
	}
	var outlet store.Record
	var b strings.Builder
	pattern, last := e.devicePattern(m, a.Org), 0
	for _, loc := range fieldTokenRe.FindAllStringSubmatchIndex(pattern, -1) {
		path := pattern[loc[0]+1 : loc[1]-1]
		val := ""
		switch {
		case path == "device.code":
			val = a.Device.Code
		case strings.HasPrefix(path, "outlet."):
			if outlet == nil {
				om := e.Reg().Model("outlet")
				if om == nil {
					return 0, deviceNameInvalid("Pola nomor " + m.Name + " memakai outlet, tapi model outlet tidak terpasang")
				}
				rec, err := store.Get(ctx, q, e.Reg(), a.Org, om, a.Device.Outlet)
				if errors.Is(err, store.ErrNotFound) {
					return 0, deviceNameInvalid("Outlet perangkat " + a.Device.Outlet + " tidak ditemukan")
				}
				if err != nil {
					return 0, err
				}
				outlet = rec
			}
			val = str(outlet[strings.TrimPrefix(path, "outlet.")])
		default:
			return 0, deviceNameInvalid("Pola nomor " + m.Name + " memakai {" + path + "}, yang tidak bisa diisi perangkat")
		}
		if !tokenValue.MatchString(val) {
			return 0, deviceNameInvalid("Nomor dokumen memakai {" + path + "} = \"" + val + "\": hanya huruf besar A-Z, angka dan tanda - di antaranya")
		}
		b.WriteString(pattern[last:loc[0]])
		b.WriteString(val)
		last = loc[1]
	}
	b.WriteString(pattern[last:])
	prefix, digits := store.ExpandPattern(b.String(), at)
	want := prefix + strings.Repeat("#", digits)
	num, ok := strings.CutPrefix(name, prefix)
	if !ok || !digitsRe.MatchString(num) {
		return 0, deviceNameInvalid("Nomor " + name + " tidak sesuai pola perangkat ini (" + want + ")")
	}
	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil || n < 1 || store.FormatNumber(prefix, "", digits, n) != name {
		return 0, deviceNameInvalid("Nomor " + name + " tidak sesuai pola perangkat ini (" + want + ")")
	}
	return n, nil
}
