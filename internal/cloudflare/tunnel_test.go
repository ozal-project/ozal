package cloudflare

import "testing"

func TestPlanTunnel(t *testing.T) {
	const origin = "http://127.0.0.1:8888"
	local := &TunnelRecord{AccountID: "acc", ID: "c0ffee00-0000-4000-8000-000000000001", Name: "ozal"}
	healthy := TunnelObserved{Local: local, Read: true, Exists: true, Remote: true, TokenSaved: true, Ingress: DesiredIngress(origin), Status: "healthy", Connections: 4}
	type want struct{ tunnel, ingress, connector TunnelState }
	for _, tc := range []struct {
		name   string
		mutate func(*TunnelObserved)
		want   want
		action TunnelAction
	}{
		{"nothing created", func(o *TunnelObserved) { *o = TunnelObserved{} }, want{TunnelTodo, TunnelWaiting, TunnelWaiting}, CreateTunnel},
		{"ready", func(*TunnelObserved) {}, want{TunnelDone, TunnelDone, TunnelDone}, ""},
		{"API silent", func(o *TunnelObserved) { o.Read = false }, want{TunnelUnknown, TunnelUnknown, TunnelWaiting}, ""},
		{"deleted in dashboard", func(o *TunnelObserved) { o.Exists = false }, want{TunnelTodo, TunnelWaiting, TunnelWaiting}, CreateTunnel},
		{"locally managed tunnel is not touched", func(o *TunnelObserved) { o.Remote = false }, want{TunnelBlocked, TunnelBlocked, TunnelDone}, ""},
		{"token file lost", func(o *TunnelObserved) { o.TokenSaved = false }, want{TunnelTodo, TunnelDone, TunnelWaiting}, CreateTunnel},
		{"ingress elsewhere", func(o *TunnelObserved) {
			o.Ingress = []IngressRule{{Hostname: "a.example.com", Service: "http://localhost:3000"}, {Service: "http_status:404"}}
		}, want{TunnelDone, TunnelTodo, TunnelDone}, SetIngress},
		{"trailing slash is the same origin", func(o *TunnelObserved) { o.Ingress = []IngressRule{{Service: origin + "/"}} }, want{TunnelDone, TunnelDone, TunnelDone}, ""},
		{"connector offline", func(o *TunnelObserved) { o.Status, o.Connections = "inactive", 0 }, want{TunnelDone, TunnelDone, TunnelTodo}, ""},
		{"degraded still serves", func(o *TunnelObserved) { o.Status, o.Connections = "degraded", 1 }, want{TunnelDone, TunnelDone, TunnelDone}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := healthy
			tc.mutate(&o)
			steps := PlanTunnel(o, origin)
			by := map[TunnelStepName]TunnelStep{}
			for _, step := range steps {
				by[step.Name] = step
			}
			got := want{by[TunnelExists].State, by[TunnelIngress].State, by[TunnelConnector].State}
			if got != tc.want {
				t.Fatalf("states = %+v, want %+v", got, tc.want)
			}
			var action TunnelAction
			for _, s := range steps {
				if s.Action != "" && action == "" {
					action = s.Action
				}
			}
			if action != tc.action {
				t.Fatalf("first action = %q, want %q", action, tc.action)
			}
			if TunnelReady(steps) != (tc.want == want{TunnelDone, TunnelDone, TunnelDone}) {
				t.Fatal("TunnelReady disagrees with the steps")
			}
			if c := by[TunnelConnector]; c.State == TunnelTodo && c.Manual != ConnectorCommand {
				t.Fatalf("connector step lacks the command: %+v", c)
			}
		})
	}
}

func TestPlanTunnelSignInRecord(t *testing.T) {
	const origin = "http://127.0.0.1:8888"
	local := &TunnelRecord{AccountID: "acc", ID: "C0FFEE00-1", Name: "ozal"}
	base := TunnelObserved{Local: local, Read: true, Exists: true, Remote: true, TokenSaved: true, Ingress: DesiredIngress(origin), Status: "healthy", Connections: 4, LoginHost: "auth.example.com", LoginRead: true}
	for _, tc := range []struct {
		name   string
		mutate func(*TunnelObserved)
		state  TunnelState
		action TunnelAction
	}{
		{"missing record is created", func(*TunnelObserved) {}, TunnelTodo, CreateLoginRecord},
		{"pointing at the tunnel", func(o *TunnelObserved) { o.LoginRecords = []string{"CNAME c0ffee00-1.cfargotunnel.com"} }, TunnelDone, ""},
		{"another record is never replaced", func(o *TunnelObserved) { o.LoginRecords = []string{"A 203.0.113.10"} }, TunnelBlocked, ""},
		{"unreadable", func(o *TunnelObserved) { o.LoginRead = false }, TunnelUnknown, ""},
		{"before the tunnel", func(o *TunnelObserved) { o.Local = nil }, TunnelWaiting, ""},
		{"no passkey login", func(o *TunnelObserved) { o.LoginHost = "" }, TunnelDone, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := base
			tc.mutate(&o)
			var login TunnelStep
			for _, s := range PlanTunnel(o, origin) {
				if s.Name == TunnelLogin {
					login = s
				}
			}
			if login.State != tc.state || login.Action != tc.action {
				t.Fatalf("login step = %+v", login)
			}
		})
	}
	if TunnelReady(PlanTunnel(base, origin)) {
		t.Fatal("ready without the sign-in record")
	}
}
