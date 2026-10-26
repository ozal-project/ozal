package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func runConsole(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: switchboard console login|list|revoke [flags]")
	}
	fs := flag.NewFlagSet("console "+args[0], flag.ContinueOnError)
	fs.SetOutput(out)
	af := addAdminFlags(fs)
	name := fs.String("name", "", "operator name asserted by host authority")
	id := fs.String("id", "", "session id to revoke")
	origin := fs.String("url", "http://127.0.0.1:9090", "private browser URL (use the locally forwarded port for a remote edge)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected console arguments")
	}
	c, err := af.client()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(*af.target, "unix:") && c.base != "http://localhost" {
		return errors.New("console credentials are issued on the host admin Unix socket only")
	}
	switch args[0] {
	case "login":
		if strings.TrimSpace(*name) == "" {
			return errors.New("login needs -name OPERATOR")
		}
		base, err := adminBase(*origin)
		if err != nil {
			return err
		}
		var answer struct {
			Token   string    `json:"token"`
			Expires time.Time `json:"expires"`
		}
		if err := c.do(ctx, http.MethodPost, "/_/api/operators", map[string]string{"name": *name}, &answer); err != nil {
			return err
		}
		fmt.Fprintf(out, "Open once within five minutes on the private console (do not share this link):\n%s/overview#operator=%s\n", base, url.QueryEscape(answer.Token))
	case "list":
		var answer struct {
			Sessions []struct {
				ID      string    `json:"id"`
				Name    string    `json:"name"`
				Expires time.Time `json:"expires"`
			} `json:"sessions"`
		}
		if err := c.do(ctx, http.MethodGet, "/_/api/operators", nil, &answer); err != nil {
			return err
		}
		for _, session := range answer.Sessions {
			fmt.Fprintf(out, "%s\t%s\t%s\n", session.ID, session.Name, session.Expires.Format(time.RFC3339))
		}
	case "revoke":
		if *id == "" {
			return errors.New("revoke needs -id ID")
		}
		if err := c.do(ctx, http.MethodDelete, "/_/api/operators/"+url.PathEscape(*id), nil, nil); err != nil {
			return err
		}
		fmt.Fprintln(out, "Operator session revoked")
	default:
		return errors.New("unknown console command")
	}
	return nil
}
