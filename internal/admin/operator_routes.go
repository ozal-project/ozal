package admin

import (
	"github.com/switchboard-project/switchboard/internal/config"
	"net/http"
)

// A temporary browser credential may manage access and destinations, but
// cannot choose a new local filesystem root and turn host secrets into a
// public site. Existing host-configured roots may only be retained verbatim.
func (h *Handler) allowOperatorRoot(w http.ResponseWriter, r *http.Request, route config.Route, existing bool) bool {
	if _, temporary := r.Context().Value(operatorKey{}).(operatorGrant); !temporary || route.Root == "" {
		return true
	}
	if existing {
		host, err := config.NormalizeHost(route.Host)
		if err == nil {
			for _, current := range h.manager.Config().Routes {
				if current.Host == host && current.Root == route.Root {
					return true
				}
			}
		}
	}
	writeError(w, 403, "temporary operator sessions cannot choose a local filesystem root; publish a Pages artifact or configure the content root on the host")
	return false
}
