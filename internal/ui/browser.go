package ui

import (
	"context"
	"errors"
	"net/url"
	"os/exec"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// openTimeout bounds one browser start.
const openTimeout = 10 * time.Second

// The browser's notices.
const noticeLinkRefused = "not opening that link: only http and https links open"

// urlOpener opens a link in the operator's browser.
type urlOpener func(ctx context.Context, url string) error

// browserDoneMsg answers an opener that failed.
type browserDoneMsg struct{ err error }

// openBrowser runs the platform's opener on raw: no shell, no stdio, its own
// session. At the timeout the opener process is killed and its detached children survive.
func openBrowser(ctx context.Context, raw string) error {
	argv, err := browserArgv(raw)
	if err != nil {
		return err
	}

	//nolint:gosec // argv is the platform opener and a link linkOK passed; no shell
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)

	err = startDetached(cmd)

	switch ctxErr := ctx.Err(); {
	case errors.Is(ctxErr, context.DeadlineExceeded):
		return nil
	case ctxErr != nil:
		return ctxErr
	}

	return err
}

// linkOK reports whether raw is an http or https link with a host and no
// control, space or invisible character.
func linkOK(raw string) bool {
	if strings.IndexFunc(raw, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) }) >= 0 {
		return false
	}

	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return false
	}

	return strings.EqualFold(u.Scheme, "https") || strings.EqualFold(u.Scheme, "http")
}

// openURL opens raw in the browser off the loop. A link that is not http or
// https with a host is refused with a notice.
func (a *App) openURL(raw string) tea.Cmd {
	if !linkOK(raw) {
		a.notice = noticeLinkRefused

		return nil
	}

	var (
		open   = a.openLink
		appCtx = a.ctx
	)

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(appCtx, openTimeout)
		defer cancel()

		if err := open(ctx, raw); err != nil && !errors.Is(err, context.Canceled) {
			return browserDoneMsg{err: err}
		}

		return nil
	}
}

// onBrowserDone says why the browser did not open.
func (a *App) onBrowserDone(msg browserDoneMsg) {
	a.toast(levelError, "browser: "+gitReason(msg.err))
}
