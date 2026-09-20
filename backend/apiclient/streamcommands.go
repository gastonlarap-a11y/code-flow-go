package apiclient

import (
	"context"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
)

// The streaming commands.
//
// Every one of them addresses a connection by the id the renderer minted, and none of them waits
// for the far side: `connect` returns once the socket is up and the pump is running, and the rest
// queue into that pump's channel. What actually happened arrives on `api:stream-message` and
// `api:stream-status`, which is the only place a live connection's history exists.

// StreamDeps is what the streaming commands need.
type StreamDeps struct {
	Streams *Streams
}

// RegisterStreams adds the WebSocket and Socket.IO commands, and the disconnect all three
// transports share.
func RegisterStreams(r *bridge.Registry, deps StreamDeps) {
	r.Add("api_ws_connect", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		request, err := bridge.Arg[WsConnectRequest](p, "request")
		if err != nil {
			return nil, err
		}
		// The pump detaches from this context inside `register`: it has to outlive the command by
		// as long as the socket lives.
		return nil, deps.Streams.ConnectWebSocket(ctx, id, request)
	})

	r.Add("api_ws_send", func(_ context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		payload, err := bridge.Arg[string](p, "payload")
		if err != nil {
			return nil, err
		}
		binary, err := bridge.Arg[bool](p, "binary")
		if err != nil {
			return nil, err
		}
		return nil, deps.Streams.Send(id, payload, binary)
	})

	r.Add("api_socketio_connect", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		request, err := bridge.Arg[SocketIoConnectRequest](p, "request")
		if err != nil {
			return nil, err
		}
		return nil, deps.Streams.ConnectSocketIO(ctx, id, request)
	})

	r.Add("api_socketio_emit", func(_ context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		event, err := bridge.Arg[string](p, "event")
		if err != nil {
			return nil, err
		}
		payload, err := bridge.Arg[string](p, "payloadJson")
		if err != nil {
			return nil, err
		}
		return nil, deps.Streams.Emit(id, event, payload)
	})

	registerMQTT(r, deps)

	// Shared by all three transports, and **safe on an unknown id**: a disconnect can legitimately
	// race a connection that already died on its own.
	r.Add("api_stream_disconnect", func(_ context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		deps.Streams.Close(id)
		return nil, nil
	})
}

// registerMQTT adds the four broker commands.
//
// Each one queues into the connection's own command goroutine and returns; what the broker made of
// it arrives on the transcript, like every other streaming result.
func registerMQTT(r *bridge.Registry, deps StreamDeps) {
	r.Add("api_mqtt_connect", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		request, err := bridge.Arg[MqttConnectRequest](p, "request")
		if err != nil {
			return nil, err
		}
		return nil, deps.Streams.ConnectMQTT(ctx, id, request)
	})

	r.Add("api_mqtt_publish", func(_ context.Context, p bridge.Params) (any, error) {
		id, topic, err := mqttTarget(p)
		if err != nil {
			return nil, err
		}
		payload, err := bridge.Arg[string](p, "payload")
		if err != nil {
			return nil, err
		}
		qos, err := bridge.Arg[int64](p, "qos")
		if err != nil {
			return nil, err
		}
		retain, err := bridge.Arg[bool](p, "retain")
		if err != nil {
			return nil, err
		}
		return nil, deps.Streams.Publish(id, topic, payload, qosByte(qos), retain)
	})

	r.Add("api_mqtt_subscribe", func(_ context.Context, p bridge.Params) (any, error) {
		id, topic, err := mqttTarget(p)
		if err != nil {
			return nil, err
		}
		qos, err := bridge.Arg[int64](p, "qos")
		if err != nil {
			return nil, err
		}
		return nil, deps.Streams.Subscribe(id, topic, qosByte(qos))
	})

	r.Add("api_mqtt_unsubscribe", func(_ context.Context, p bridge.Params) (any, error) {
		id, topic, err := mqttTarget(p)
		if err != nil {
			return nil, err
		}
		return nil, deps.Streams.Unsubscribe(id, topic)
	})
}

// mqttTarget reads the two parameters every broker command takes.
func mqttTarget(p bridge.Params) (id, topic string, err error) {
	if id, err = bridge.Arg[string](p, "id"); err != nil {
		return "", "", err
	}
	if topic, err = bridge.Arg[string](p, "topic"); err != nil {
		return "", "", err
	}
	return id, topic, nil
}

// qosByte narrows the renderer's `number` without wrapping: a negative or enormous value becomes
// something above 2, which `clampQoS` then reduces to 0 — the same degradation a corrupt stored
// value gets, rather than a wrap that would turn -1 into a valid QoS 255 → 0 by accident.
func qosByte(qos int64) byte {
	if qos < 0 || qos > 2 {
		return 3
	}
	return byte(qos)
}
