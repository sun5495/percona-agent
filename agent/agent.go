package agent

import (
	"os"
	"os/user"
	"time"
	"fmt"
	"sync"
	"encoding/json"
	"github.com/percona/percona-cloud-tools/agent/log"
	"github.com/percona/percona-cloud-tools/agent/proto"
	"github.com/percona/percona-cloud-tools/agent/service"
)

const (
	CMD_QUEUE_SIZE = 10
	STATUS_QUEUE_SIZE = 100
)

type Agent struct {
	config *Config
	cmdClient proto.Client
	statusClient proto.Client
	cc *ControlChannels
	services map[string]service.Manager
	// --
	log *log.LogWriter
	cmdq []*proto.Msg
	statusq []*proto.Msg
	status string
	m map[string]*sync.Mutex
}

func NewAgent(config *Config, cc *ControlChannels, cmdClient proto.Client, statusClient proto.Client, services map[string]service.Manager) *Agent {
	agent := &Agent{
		config: config,
		cc: cc,
		cmdClient: cmdClient,
		statusClient: statusClient,
		services: services,
		// --
		log: log.NewLogWriter(cc.LogChan, "pct-agentd"),
		cmdq: make([]*proto.Msg, CMD_QUEUE_SIZE),
		statusq: make([]*proto.Msg, STATUS_QUEUE_SIZE),
		m: map[string]*sync.Mutex{
			"agent": new(sync.Mutex),
			"cmd": new(sync.Mutex),
			"status": new(sync.Mutex),
		},
	}
	return agent
}

func (agent *Agent) Hello() map[string] string {
	var data map[string]string
	u, _ := user.Current()
	//data["agent_uuid"] = agent.Uuid
	data["hostname"], _ = os.Hostname()
	data["username"] = u.Username
	return data
}

func (agent *Agent) Run() {
	agent.log.Info("Running agent")

	/*
	 * Start the status and cmd handlers.  Most messages must be serialized because,
	 * for example, handling start-service and stop-service at the same
	 * time would cause weird problems.  The cmdChan serializes messages,
	 * so it's "first come, first serve" (i.e. fifo).  Concurrency has
	 * consequences: e.g. if user1 sends a start-service and it succeeds
	 * and user2 send the same start-service, user2 will get a ServiceIsRunningError.
	 *
	 * Status requests are handled concurrently so the user can always see what
	 * the agent is doing even if it's busy processing commands.
	 */
	agent.statusClient.Run()
	recvStatusChan := agent.statusClient.RecvChan()
	statusChan := make(chan *proto.Msg, STATUS_QUEUE_SIZE)
	go agent.statusHandler(statusChan)

	agent.cmdClient.Run()
	recvCmdChan := agent.cmdClient.RecvChan()
	cmdChan := make(chan *proto.Msg, CMD_QUEUE_SIZE)
	doneChan := make(chan bool, 1)
	go agent.cmdHandler(cmdChan, doneChan)

	// Reject new msgs if either of these are true.
	exitPending := false
	updatePending := false

	// Receive and handle cmd and status requests from the API.
	for {
		agent.setStatus(nil, "Wait listen")

		select {
		case msg := <-recvCmdChan: // from API (wss:/cmd)
			if exitPending {
			} else if updatePending {
			} else {
				// Try to send the cmd to the cmdHandler.  If the cmdq is not full,
				// this will not block, else the default case will be called and we
				// return a queue full error to let the user know that the agent is
				// too busy.
				select {
					case cmdChan <-msg:
					default:
						// @todo return quque full error
				}
			}

			// Remember if this command is exit or update so we can reject subsequent commands.
			if msg.Cmd == "exit" {
				exitPending = true
				close(cmdChan)
			} else if msg.Cmd == "update" {
				updatePending = true
				close(cmdChan)
			}
		case <-doneChan: // from cmdHandler
			break
		case msg := <-recvStatusChan: // from API (wss:/status)
			select {
				case statusChan <-msg:
				default:
					// @todo return quque full error
			}
		case <-agent.cc.StopChan: // from caller
			close(cmdChan)
			break
		}
	}

	close(statusChan)

	if exitPending {
		os.Exit(0)
	} else if updatePending {
		agent.selfUpdate()
	}

	// Shouldn't get here.
	// @todo return error
	os.Exit(1)
}

func (agent *Agent) cmdHandler(cmdChan chan *proto.Msg, doneChan chan bool) {
	sendReplyChan := agent.cmdClient.SendChan()

	for msg := range cmdChan {
		// Append the msg to the queue; this is just for status requests.
		agent.m["cmd"].Lock()
		agent.cmdq = append(agent.cmdq, msg)
		agent.m["cmd"].Unlock()

		// Run the command in another goroutine so we can wait for it
		// (and possibly timeout) in this goroutine.
		cmdDone := make(chan error)
		go func() {
			var err error
			switch {
			case msg.Cmd == "SetConfig":
				err = agent.handleSetConfig(msg)
			case msg.Cmd == "StartService":
				err = agent.handleStartService(msg)
			case msg.Cmd == "StopService":
				err = agent.handleStopService(msg)
			default:
				err = UnknownCmdError{Cmd:msg.Cmd}
			}
			cmdDone <- err
		}()

		// Wait for the cmd to complete.
		var err error
		cmdTimeout := time.After(time.Duration(msg.Timeout) * time.Second)
		for {
			select {
			case err = <-cmdDone:
				break
			case <-cmdTimeout:
				err = CmdTimeoutError{Cmd:msg.Cmd}
			}
		}

		// Reply to the command: just the error if any.  The user can check
		// the log for details about running the command because the msg
		// should have been associated with the log entries in the cmd handler
		// function by calling LogWriter.Re().
		agent.setStatus(msg, "Replying to " + msg.Cmd)
		sendReplyChan <-msg.Reply(proto.CmdReply{Error: err})

		// Pop the msg from the queue; this is just for status requests.
		agent.m["cmd"].Lock()
		agent.cmdq = agent.cmdq[0:len(agent.cmdq) - 1]
		agent.m["cmd"].Unlock()
	}

	// Caller closed cmdChan and we're done handling the commands.
	doneChan <-true
}

func (agent *Agent) statusHandler(statusChan chan *proto.Msg) {
	sendStatusChan := agent.statusClient.SendChan()

	for msg := range statusChan {

		agent.m["status"].Lock()
		agent.statusq = append(agent.statusq, msg)
		agent.m["status"].Unlock()

		status := new(proto.StatusReply)

		agent.m["agent"].Lock()
		agent.m["cmdq"].Lock()

		status.Agent = agent.status

		status.CmdQueue = make([]string, len(agent.cmdq))
		for _, msg := range agent.cmdq {
			status.CmdQueue = append(status.CmdQueue, msg.String())
		}

		status.Service = make(map[string]string)
		for service, m := range agent.services {
			status.Service[service] = m.Status()
		}

		agent.m["cmdq"].Unlock()
		agent.m["agent"].Unlock()

		sendStatusChan <-msg.Reply(status)

		agent.m["status"].Lock()
		agent.statusq = agent.statusq[0:len(agent.statusq) - 1]
		agent.m["status"].Unlock()
	}
}

/////////////////////////////////////////////////////////////////////////////
// proto.Msg.Cmd handlers
/////////////////////////////////////////////////////////////////////////////

func (agent *Agent) handleSetConfig(msg *proto.Msg) error {
	agent.setStatus(msg, "Setting config")
	return nil
}

func (agent *Agent) handleStartService(msg *proto.Msg) error {
	agent.log.Debug("Agent.startService")

	// Unmarshal the data to get the service name and config.
	s := new(proto.ServiceMsg)
	if err := json.Unmarshal(msg.Data, s); err != nil {
		return err
	}

	// Check if we have a manager for the service.
	m, ok := agent.services[s.Name]
	if !ok {
		return UnknownServiceError{Service:s.Name}
	}

	// Return error if service is running.  To keep things simple,
	// we do not restart the service or verifty that the given config
	// matches the running config.  Only stopped services can be started.
	if m.IsRunning() {
		return service.ServiceIsRunningError{Service:s.Name}
	}

	// Start the service with the given config.
	agent.setStatus(msg, "Starting service " + s.Name)
	err := m.Start(msg, s.Config)
	return err
}

func (agent *Agent) handleStopService(msg *proto.Msg) error {
	agent.log.Debug("Agent.stopService")

	// Unmarshal the data to get the service name.
	s := new(proto.ServiceMsg)
	if err := json.Unmarshal(msg.Data, s); err != nil {
		return err
	}

	// Check if we have a manager for the service.  If not, that's ok,
	// just return because the service can't be running if we don't have it.
	m, ok := agent.services[s.Name]
	if !ok {
		return nil
	}

	// If the service is not running, then return.  Stopping a service
	// is an idempotent operation.
	if !m.IsRunning() {
		return nil
	}

	// Stop the service.
	agent.setStatus(msg, "Stopping service " + s.Name)
	err := m.Stop(msg)
	return err
}

/////////////////////////////////////////////////////////////////////////////
// Internal methods
/////////////////////////////////////////////////////////////////////////////

func (agent *Agent) setStatus(msg *proto.Msg, status ...interface{}) {
	agent.m["agent"].Lock()
	agent.status = fmt.Sprintf("[%s] %v", msg, status)
	agent.m["agent"].Unlock()
}

func (agent *Agent) stopAllServices() {
}

func (agent *Agent) selfUpdate() {
}
