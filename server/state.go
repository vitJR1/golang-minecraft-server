package server

type State int

const (
	StateHandshake State = iota
	StateStatus
	StateLogin
	StatePlay
)

func (s State) String() string {
	switch s {
	case StateHandshake:
		return "handshake"
	case StateStatus:
		return "status"
	case StateLogin:
		return "login"
	case StatePlay:
		return "play"
	default:
		return "unknown"
	}
}
