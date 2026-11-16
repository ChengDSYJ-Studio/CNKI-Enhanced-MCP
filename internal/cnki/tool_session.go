// Tools login and status.

package cnki

import (
	"context"
	"strings"
	"time"
)

func (a *App) Login(ctx context.Context, in LoginArgs) (any, error) {
	entry := advancedURL
	if in.RemoteAccessURL != "" {
		if !strings.HasPrefix(in.RemoteAccessURL, "https://") || strings.Contains(strings.SplitN(in.RemoteAccessURL[8:], "/", 2)[0], "@") {
			return nil, fail("INVALID_LOGIN_URL", "登录入口须为不含凭据的 HTTPS 地址")
		}
		entry = in.RemoteAccessURL
	}
	return a.run(ctx, "login", Budget{MaxRequests: 20, TimeoutSeconds: 1800}, nil, func(op *Op) (any, error) {
		s := a.Site
		page, err := s.browser.NewPage(op.ctx)
		if err != nil {
			return nil, err
		}
		defer closePage(page)
		if err = s.open(op, page, entry, 20*time.Second, func(pageGate) (bool, error) { return true, nil }); err != nil {
			return nil, err
		}
		if in.RemoteAccessURL == "" {
			g, err := s.waitIdentity(op, page)
			if err != nil {
				return nil, err
			}
			if !in.Force && (g.Personal || len(g.Institutions) > 0) {
				return s.rememberIdentity(g, "already_logged_in"), nil
			}
		}
		// The person logs in directly in the browser; credentials never pass through MCP.
		if err = s.browser.Window(op.ctx, page, "normal", true); err != nil {
			return nil, err
		}
		op.awaitUser("请在弹出的浏览器窗口完成知网登录；登录成功后自动继续（机构远程入口完成后调用 status(operation_id, user_done=true)）")
		resume := op.resumeSignal()
		deadline := time.Now().Add(20 * time.Minute)
		var g pageGate
		for found := false; !found; {
			if time.Now().After(deadline) {
				return nil, &Problem{Code: "LOGIN_TIMEOUT", Message: "等待登录超时；需要时重新调用 login", Retryable: true}
			}
			select {
			case <-op.ctx.Done():
				return nil, op.ctx.Err()
			case <-resume:
				found = true
			case <-time.After(1500 * time.Millisecond):
				g, found = s.identityAnywhere(op)
			}
		}
		op.userDone()
		_ = s.browser.Window(op.ctx, page, "minimized", false)
		// Confirm on a freshly loaded KNS page, where searches and downloads run.
		if err = s.open(op, page, advancedURL, 20*time.Second, func(pageGate) (bool, error) { return true, nil }); err == nil {
			if confirmed, e := s.waitIdentity(op, page); e == nil && (confirmed.Personal || len(confirmed.Institutions) > 0) {
				g = confirmed
			}
		}
		return s.rememberIdentity(g, "browser_login"), nil
	})
}

func (a *App) Status(ctx context.Context, in StatusArgs) (any, error) {
	if err := a.init(); err != nil {
		return nil, err
	}
	switch {
	case in.Cancel:
		return a.Jobs.Cancel(ctx, in.OperationID) // empty: this client's active operation
	case in.UserDone:
		if err := a.Jobs.Resume(ctx, in.OperationID); err != nil {
			return nil, err
		}
		return a.Jobs.Status(ctx, in.OperationID, 3*time.Second)
	case in.Show:
		page := a.Site.HumanPage(in.OperationID)
		if page == nil {
			return nil, fail("VERIFICATION_NOT_FOUND", "没有等待人工处理的窗口")
		}
		return map[string]any{"shown": true}, a.Site.browser.Window(ctx, page, "normal", true)
	case in.Recover:
		a.Site.recover()
		a.Site.browser.Reset()
		return map[string]any{"recovered": true, "message": "浏览器将在下一次请求时重新启动"}, nil
	case in.CheckLogin:
		return a.run(ctx, "session", Budget{MaxRequests: 10, TimeoutSeconds: 60}, nil, func(op *Op) (any, error) {
			page, err := a.Site.browser.NewPage(op.ctx)
			if err != nil {
				return nil, err
			}
			defer closePage(page)
			if err = a.Site.open(op, page, advancedURL, 20*time.Second, func(pageGate) (bool, error) { return true, nil }); err != nil {
				return nil, err
			}
			g, err := a.Site.waitIdentity(op, page)
			if err != nil {
				return nil, err
			}
			return a.Site.rememberIdentity(g, "checked"), nil
		})
	case in.OperationID != "":
		wait := 50
		if in.WaitSeconds > 0 {
			wait = min(in.WaitSeconds, 55)
		}
		return a.Jobs.Status(ctx, in.OperationID, time.Duration(wait)*time.Second)
	}
	out := map[string]any{"login": a.Site.identityView(), "data_dir": a.Root}
	if op, ok := a.Jobs.forCaller(ctx, ""); ok {
		out["active_operation"] = compactOp(op.view())
	}
	if in.Details {
		out["diagnostics"] = a.Diagnostics()
	}
	return out, nil
}
