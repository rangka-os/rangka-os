package meta

import "testing"

// TestMemberScoped: every || branch of a member grant's if: must hold <field> == user.name (contract 0074 §2).
func TestMemberScoped(t *testing.T) {
	for cond, want := range map[string]bool{
		"employee == user.name": true,
		"user.name == employee": true,
		"employee == user.name && docstatus == 1 && (payroll_run.closed || (payroll_run.pay_date != '' && payroll_run.pay_date <= today()))": true,
		"requested_by == user.name || employee == user.name": true,
		"(employee == user.name) && published":               true,
		"user.name != ''":                                    false,
		"employee == user.name || released":                  false,
		"employee != user.name":                              false,
		"released":                                           false,
		"":                                                   false,
		"(employee == user.name || true)":                    false,
		"name == 'x' && 'employee == user.name' != ''":       false,
	} {
		if got := MemberScoped(cond); got != want {
			t.Errorf("MemberScoped(%q) = %v, want %v", cond, got, want)
		}
	}
}
