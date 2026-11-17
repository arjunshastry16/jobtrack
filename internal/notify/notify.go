// Package notify sends short push notifications (e.g. follow-up reminders).
//
// Code that sends notifications depends only on the Notifier interface. Ntfy delivers
// them through an ntfy server (https://ntfy.sh or self-hosted) to the ntfy phone app;
// Log just writes them to the log (for development, or when no topic is configured);
// notifytest.Recorder captures them in tests.
package notify

import (
	"context"
	"log/slog"
)

// Priority maps to ntfy's priority levels (which control sound and vibration on the phone).
type Priority string

// Priorities, lowest to highest.
const (
	PriorityMin     Priority = "min"
	PriorityLow     Priority = "low"
	PriorityDefault Priority = "default"
	PriorityHigh    Priority = "high"
	PriorityUrgent  Priority = "urgent"
)

// Message is one notification. Keep it short and free of sensitive detail: on a public
// ntfy server anyone who knows the topic name can read it.
type Message struct {
	Title    string
	Body     string
	Priority Priority // empty means default
	Tags     []string // ntfy shows known tags as emoji, e.g. "bell", "warning"
	ClickURL string   // opened when the notification is tapped (optional)
}

// Notifier delivers messages.
type Notifier interface {
	Notify(ctx context.Context, m Message) error
}

// Log is a Notifier that only logs messages.
type Log struct {
	Logger *slog.Logger
}

// Notify implements Notifier.
func (l Log) Notify(ctx context.Context, m Message) error {
	l.Logger.InfoContext(ctx, "notification (log only, no ntfy topic configured)",
		"title", m.Title, "body", m.Body, "priority", string(m.Priority))
	return nil
}
