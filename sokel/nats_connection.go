// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"errors"
	"log"
	"net"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
)

// natsConnectionOptions keeps the connection's recovery behavior together for all Go plugins.
func natsConnectionOptions(name string, acc access, violation *atomic.Value) []nats.Option {
	return []nats.Option{
		nats.UserInfo(acc.User, acc.Pass), nats.Name(name), nats.MaxReconnects(-1),
		nats.RetryOnFailedConnect(true), nats.ReconnectWait(2 * time.Second),
		// A stalled write holds the NATS connection lock, so the default one-minute write
		// deadline also stalls requests and liveness checks. Bound that contention so recovery can start promptly.
		nats.FlusherTimeout(5 * time.Second),
		nats.PingInterval(10 * time.Second), nats.MaxPingsOutstanding(2),
		nats.DisconnectErrHandler(func(_ *nats.Conn, derr error) {
			log.Printf("[sokel] disconnected from the platform: %v (reconnecting)", derr)
		}),
		nats.ReconnectHandler(func(c *nats.Conn) { log.Printf("[sokel] reconnected to the platform: %s", c.ConnectedUrl()) }),
		nats.ErrorHandler(func(c *nats.Conn, sub *nats.Subscription, aerr error) {
			if errors.Is(aerr, nats.ErrPermissionViolation) {
				violation.Store(aerr.Error())
				log.Printf("[sokel] %v — %s", aerr, violationHint)
				return
			}
			log.Printf("[sokel] nats error: %v", aerr)
			// nats.go 1.48 reports background socket write failures without reconnecting.
			// Only transport failures warrant replacing the socket; a request timeout or
			// slow subscription must leave a healthy connection intact. LastError is cleared
			// on reconnect, so ignore callbacks queued before a connection already recovered.
			var opErr *net.OpError
			if sub == nil && errors.As(aerr, &opErr) && opErr.Op == "write" && c.IsConnected() && c.LastError() != nil {
				log.Printf("[sokel] socket write failed; forcing broker reconnect")
				if err := c.ForceReconnect(); err != nil && !errors.Is(err, nats.ErrConnectionClosed) {
					log.Printf("[sokel] forcing broker reconnect failed: %v", err)
				}
			}
		}),
	}
}
