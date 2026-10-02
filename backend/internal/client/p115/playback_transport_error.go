package p115

// Preserve transport classification without exposing request URLs/signatures
// through the user-visible error text.
type playbackTransportError struct{ cause error }

func (e *playbackTransportError) Error() string { return "请求 115 接口连接失败" }
func (e *playbackTransportError) Unwrap() error { return e.cause }
