package daemon

import (
	"github.com/wow-look-at-my/remote-agent/protocol"
)

// Handler processes daemon requests by dispatching to the appropriate operation.
type Handler struct {
	daemon *Daemon
}

// Handle routes a request to the correct operation.
func (h *Handler) Handle(req *protocol.DaemonRequest) *protocol.DaemonResponse {
	switch req.Action {
	case "ping":
		return h.handlePing()
	case "exec":
		return h.handleExec(req.Params)
	case "read":
		return h.handleRead(req.Params)
	case "write":
		return h.handleWrite(req.Params)
	case "upload":
		return h.handleUpload(req.Params)
	case "download":
		return h.handleDownload(req.Params)
	case "edit":
		return h.handleEdit(req.Params)
	case "ls":
		return h.handleLs(req.Params)
	case "ps":
		return h.handlePs(req.Params)
	case "sysinfo":
		return h.handleSysinfo()
	case "disconnect":
		return h.handleDisconnect()
	default:
		return &protocol.DaemonResponse{Error: "unknown action: " + req.Action}
	}
}

func okResponse(data any) *protocol.DaemonResponse {
	return &protocol.DaemonResponse{OK: true, Data: data}
}

func errResponse(err error) *protocol.DaemonResponse {
	return &protocol.DaemonResponse{Error: err.Error()}
}
