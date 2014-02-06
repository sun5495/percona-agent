package mock

import (
	"code.google.com/p/go.net/websocket"
	"github.com/percona/cloud-protocol/proto"
)

type WebsocketClient struct {
	testSendChan     chan *proto.Cmd
	userRecvChan     chan *proto.Cmd
	userSendChan     chan *proto.Reply
	testRecvChan     chan *proto.Reply
	testSendDataChan chan interface{}
	testRecvDataChan chan interface{}
	conn             *websocket.Conn
	ErrChan          chan error
	RecvError        chan error
	connectChan      chan bool
	testConnectChan  chan bool
	started          bool
	RecvBytes        chan []byte
}

func NewWebsocketClient(sendChan chan *proto.Cmd, recvChan chan *proto.Reply, sendDataChan chan interface{}, recvDataChan chan interface{}) *WebsocketClient {
	c := &WebsocketClient{
		testSendChan:     sendChan,
		userRecvChan:     make(chan *proto.Cmd, 10),
		userSendChan:     make(chan *proto.Reply, 10),
		testRecvChan:     recvChan,
		testSendDataChan: sendDataChan,
		testRecvDataChan: recvDataChan,
		conn:             new(websocket.Conn),
		RecvError:        make(chan error),
		connectChan:      make(chan bool, 1),
		RecvBytes:        make(chan []byte, 1),
	}
	return c
}

func (c *WebsocketClient) Connect() {
	if c.testConnectChan != nil {
		// Wait for test to let user/agent connect.
		select {
		case c.testConnectChan <- true:
		default:
		}
		<-c.testConnectChan
	}
	c.connectChan <- true
}

func (c *WebsocketClient) Disconnect() error {
	c.connectChan <- false
	return nil
}

func (c *WebsocketClient) Start() {
	if c.started {
		return
	}

	go func() {
		for cmd := range c.testSendChan { // test sends cmd
			c.userRecvChan <- cmd // user receives cmd
		}
	}()

	go func() {
		for reply := range c.userSendChan { // user sends reply
			c.testRecvChan <- reply // test receives reply
		}
	}()

	c.started = true
}

func (c *WebsocketClient) Stop() {
}

func (c *WebsocketClient) SendChan() chan *proto.Reply {
	return c.userSendChan
}

func (c *WebsocketClient) RecvChan() chan *proto.Cmd {
	return c.userRecvChan
}

func (c *WebsocketClient) Send(data interface{}) error {
	// Relay data from user to test.
	c.testRecvDataChan <- data
	return nil
}

func (c *WebsocketClient) SendBytes(data []byte) error {
	c.RecvBytes <- data
	return nil
}

func (c *WebsocketClient) Recv(data interface{}) error {
	// Relay data from test to user.
	select {
	case data = <-c.testSendDataChan:
	case err := <-c.RecvError:
		return err
	}
	return nil
}

func (c *WebsocketClient) ConnectChan() chan bool {
	return c.connectChan
}

func (c *WebsocketClient) ErrorChan() chan error {
	return c.ErrChan
}

func (c *WebsocketClient) Conn() *websocket.Conn {
	return c.conn
}

func (c *WebsocketClient) SetConnectChan(connectChan chan bool) {
	c.testConnectChan = connectChan
}
