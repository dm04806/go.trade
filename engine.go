package trade

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"reflect"
	"strings"
	"time"
)

const (
	version = 48
	gateway = "127.0.0.1:4001"
)

// Engine is the entry point to the IB TWS API
type Engine struct {
	// Returns a unique request id when read
	Tick chan RequestId
	// Write requests to this channel
	In chan interface{}
	// Read replies from this channel
	Out chan interface{}
	// Read errors from this channel
	Error         chan error
	client        int64
	con           net.Conn
	reader        *bufio.Reader
	input         *bytes.Buffer
	output        *bytes.Buffer
	serverTime    time.Time
	clientVersion int64
	serverVersion int64
}

type timeoutError struct {
}

func (e *timeoutError) Error() string {
	return fmt.Sprintf("tradine engine: timeout while trying to receive message")
}

func timeout() error {
	return &timeoutError{}
}

func uniqueId() chan RequestId {
	ch := make(chan RequestId)
	id := RequestId(0)
	go func() {
		for {
			ch <- id
			id += 1
		}
	}()
	return ch
}

// NewEngine takes a client id and returns a new connection 
// to IB Gateway or IB Trader Workstation.
func NewEngine(client int64) (*Engine, error) {
	con, err := net.Dial("tcp", gateway)
	if err != nil {
		return nil, err
	}

	reader := bufio.NewReader(con)
	input := bytes.NewBuffer(make([]byte, 0, 4096))
	output := bytes.NewBuffer(make([]byte, 0, 4096))
	tick := uniqueId()

	engine := Engine{
		client: client,
		Tick:   tick,
		con:    con,
		reader: reader,
		input:  input,
		output: output,
	}

	// write client version
	cver := &clientVersion{version}
	if err := engine.write(cver); err != nil {
		return nil, err
	}

	// read server version
	sver := &serverVersion{}
	if err := engine.read(sver); err != nil {
		return nil, err
	}

	// read server time
	tm := &serverTime{}
	if err := engine.read(tm); err != nil {
		return nil, err
	}

	// write client id
	id := &clientId{client}
	if err := engine.write(id); err != nil {
		return nil, err
	}

	engine.serverVersion = sver.Version
	engine.serverTime = tm.Time

	engine.Out = make(chan interface{})
	engine.In = make(chan interface{})
	engine.Error = make(chan error)

	// receiver
	go func() {
		for {
			msg, err := engine.receive()
			if err != nil {
				engine.Error <- err
				break
			}

			engine.Out <- msg
		}

		close(engine.Out)
	}()

	// sender
	go func() {
		for {
			msg := <-engine.In
			if err := engine.send(msg); err != nil {
				engine.Error <- err
				close(engine.In)
				return
			}
		}
	}()

	return &engine, nil
}

type packetError struct {
	value interface{}
	kind  reflect.Type
}

func (e *packetError) Error() string {
	return fmt.Sprintf("don't understand packet '%v' of type '%v'",
		e.value, e.kind)
}

func failPacket(v interface{}) error {
	return &packetError{
		value: v,
		kind:  reflect.ValueOf(v).Type(),
	}
}

func dump(b *bytes.Buffer) {
	s := strings.Replace(b.String(), "\000", "-", -1)
	fmt.Printf("Buffer = '%s'\n", s)
}

func (engine *Engine) send(v interface{}) error {
	type header struct {
		Code    int64
		Version int64
	}

	engine.output.Reset()

	code := msg2Code(v)
	if code == 0 {
		return failPacket(v)
	}

	// encode message type and client version
	ver := code2Version(code)
	hdr := &header{
		Code:    code,
		Version: ver,
	}

	if err := encode(engine.output, reflect.ValueOf(hdr)); err != nil {
		return err
	}

	// encode the message itself
	if err := encode(engine.output, reflect.ValueOf(v)); err != nil {
		return err
	}

	//dump(engine.output)

	if _, err := engine.con.Write(engine.output.Bytes()); err != nil {
		return err
	}

	return nil
}

func (engine *Engine) receive() (interface{}, error) {
	type header struct {
		Code    int64
		Version int64
	}

	engine.input.Reset()
	hdr := &header{}

	// decode header
	if err := decode(engine.reader, reflect.ValueOf(hdr)); err != nil {
		return nil, err
	}

	// decode message
	v := code2Msg(hdr.Code)
	if err := decode(engine.reader, reflect.ValueOf(v)); err != nil {
		return nil, err
	}

	return v, nil
}

func (engine *Engine) write(v interface{}) error {
	engine.output.Reset()

	if err := encode(engine.output, reflect.ValueOf(v)); err != nil {
		return err
	}

	if _, err := engine.con.Write(engine.output.Bytes()); err != nil {
		return err
	}

	return nil
}

func (engine *Engine) read(v interface{}) error {
	engine.input.Reset()

	if err := decode(engine.reader, reflect.ValueOf(v)); err != nil {
		return err
	}
	return nil
}
