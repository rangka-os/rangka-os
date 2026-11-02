package engine

// PermissionError means the caller may not do Action on Model. The API answers 403 permission_denied
// with model and action in the error body (contract 0010 §6); model permissions reuse it.
type PermissionError struct {
	Model, Action, Msg string
}

func (e *PermissionError) Error() string { return e.Msg }

// Denied builds the error for an action the caller may not perform.
func Denied(model, action string) *PermissionError {
	return &PermissionError{Model: model, Action: action, Msg: "Kamu tidak punya akses untuk " + action + " " + model}
}
