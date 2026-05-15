package eventbus

import (
	"github.com/nats-io/nats.go"
)

type Bus struct{ Conn *nats.Conn }

func New(conn *nats.Conn) *Bus { return &Bus{Conn: conn} }
