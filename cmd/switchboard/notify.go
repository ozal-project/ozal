package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/notify"
	"github.com/octaviosantacruz/switchboard/internal/notify/telegram"
)

// runNotify implements `switchboard notify pair`: link one Telegram chat to
// this deployment with a one-time code. The code lives only in this process,
// so pairing dies with the command; nothing is sent to the chat.
func runNotify(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: switchboard notify pair [-config config.json] [-timeout 10m]")
	}
	switch args[0] {
	case "pair":
		return runNotifyPair(ctx, args[1:], out)
	default:
		return fmt.Errorf("unknown notify subcommand %q", args[0])
	}
}

func runNotifyPair(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("notify pair", flag.ContinueOnError)
	fs.SetOutput(out)
	configPath := fs.String("config", "config.json", "path to the routing config")
	wait := fs.Duration("timeout", 10*time.Minute, "how long to wait for the /pair message")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if cfg.Notify == nil || cfg.Notify.Telegram == nil {
		return errors.New("notify.telegram is not configured")
	}
	token, err := config.ResolveSecret(cfg.Notify.Telegram.Token)
	if err != nil {
		return fmt.Errorf("notify.telegram.token: %w", err)
	}
	client, err := telegram.New(telegram.Config{Token: token, BaseURL: cfg.Notify.Telegram.APIBase})
	if err != nil {
		return err
	}
	chats, err := notify.OpenFileChats(cfg.Notify.Telegram.ChatsFile)
	if err != nil {
		return err
	}

	code, err := notify.NewPairingCode()
	if err != nil {
		return err
	}
	fmt.Fprintln(out, notify.PairingText(code))
	fmt.Fprintf(out, "Waiting up to %s; the code stops working when this command exits.\n", *wait)

	pairCtx, cancel := context.WithTimeout(ctx, *wait)
	defer cancel()
	var offset int64
	for {
		updates, err := client.GetUpdates(pairCtx, offset, 10*time.Second)
		if err != nil {
			if pairCtx.Err() != nil {
				return errors.New("the pairing code expired unused; nothing was paired")
			}
			return err
		}
		for _, update := range updates {
			offset = update.UpdateID + 1
			if update.Message == nil {
				continue
			}
			supplied, ok := notify.PairCommand(update.Message.Text)
			if !ok || !notify.CodeEqual(code, supplied) {
				continue // a wrong code neither pairs nor ends the command
			}
			if err := chats.Add(update.Message.Chat.ID); err != nil {
				return err
			}
			fmt.Fprintf(out, "Paired chat %d; Switchboard alerts will go there.\n", update.Message.Chat.ID)
			return nil
		}
	}
}
