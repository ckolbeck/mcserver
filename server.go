//Copyright 2010 Cory Kolbeck <ckolbeck@gmail.com>.
//So long as this notice remains in place, you are welcome 
//to do whatever you like to or with this code.  This code is 
//provided 'As-Is' with no warrenty expressed or implied. 
//If you like it, and we happen to meet, buy me a beer sometime

package mcserver

import (
	"bufio"
	"errors"
	"io"
	"log"
	"os/exec"
	"strings"
	"time"
)

const (
	STOPTIMEOUT = 10 //Time in seconds to wait for server to die cleanly.
	IN          = 0
	OUT         = 1
	ERR         = 2
)

type Server struct {
	In     chan<- string
	Out    <-chan string
	Err    <-chan string
	privIn chan string

	cmd       *exec.Cmd
	serverIn  io.Writer
	serverOut *bufio.Reader
	serverErr *bufio.Reader

	serverCommand string
	serverArgs    []string
	dir           string

	infoLog *log.Logger
	errLog  *log.Logger

	signals []chan bool
	alive   bool
	running bool
}

func NewServer(command string, args []string, dir string, infoLog, errLog *log.Logger) (*Server, error) {
	inChan := make(chan string, 1024)
	outChan := make(chan string, 1024)
	errChan := make(chan string, 1024)
	signals := []chan bool{make(chan bool, 1), make(chan bool, 1), make(chan bool, 1)}

	server := &Server{
		In:     inChan,
		Out:    outChan,
		Err:    errChan,
		privIn: make(chan string),

		serverCommand: command,
		serverArgs:    args,
		dir:           dir,

		infoLog: infoLog,
		errLog:  errLog,

		signals: signals,
		alive:   true,
		running: false,
	}

	go server.writeIn(inChan, signals[IN])
	go server.read(&server.serverOut, outChan, signals[OUT])
	go server.read(&server.serverErr, errChan, signals[ERR])

	return server, nil
}

func (self *Server) IsRunning() bool {
	return self.running
}

func (self *Server) Start() error {
	if self.running {
		return errors.New("Server already started.")
	}

	self.cmd = exec.Command(self.serverCommand, self.serverArgs...)
	self.cmd.Dir = self.dir

	in, e := self.cmd.StdinPipe()
	if e != nil {
		return e
	}

	out, e := self.cmd.StdoutPipe()
	if e != nil {
		return e
	}

	err, e := self.cmd.StderrPipe()
	if e != nil {
		return e
	}

	if e := self.cmd.Start(); e != nil {
		return e
	}

	self.infoLog.Printf("Server forked, pid %d\n", self.cmd.Process.Pid)

	self.serverIn = in
	self.serverOut = bufio.NewReader(out)
	self.serverErr = bufio.NewReader(err)

	for _, c := range self.signals {
		c <- true
	}

	self.infoLog.Println("Server started")
	self.running = true

	return nil
}

func (self *Server) Stop(delay time.Duration, msg string) (err error) {
	if !self.running {
		return errors.New("Server not running.")
	}
	self.running = false

	if msg != "" {
		self.privIn <- "say " + msg
	}

	if delay > 0 {
		<-(time.After(delay))
	}

	self.signals[OUT] <- false
	self.signals[ERR] <- false
	self.privIn <- "stop\n"
	self.signals[IN] <- false

	itsDeadJim := make(chan bool)

	go func() {
		err = self.cmd.Wait()
		itsDeadJim <- true
	}()

	select {
	case <-itsDeadJim:
		return
	case <-time.After(STOPTIMEOUT * time.Second):
		if err = self.cmd.Process.Kill(); err != nil {
			return err
		}
		//Process has been killed, if wait doesn't return immediately something is broken.
		err = self.cmd.Wait() 
	}

	return err
}

func (self *Server) Destroy() error {
	err := self.Stop(0, "Server going down NOW")
	self.alive = false
	close(self.In)

	return err
}

func (self *Server) GetPID() (int, error) {
	if !self.running {
		return -1, errors.New("Server not running.")
	}

	return self.cmd.Process.Pid, nil
}

func (self *Server) read(stream **bufio.Reader, writeChan chan string, signal chan bool) {
	var l []byte

	<-signal //Wait for the start signal

	for self.alive {
		select {
		case run := <-signal:
			for !run {
				run = <-signal
			}

		default:
			line, prefix, err := stream.ReadLine()
			if err == nil {
				for prefix && err == nil {
					l, prefix, err = stream.ReadLine()
					line = append(line, l...)
				}
				writeChan <- string(line)
			}
		}
	}
}

func (self *Server) writeIn(in <-chan string, signal chan bool) {
	
	<-signal //Wait for the start signal

	for self.alive {
		select {
		case cmd := <-self.privIn:
			self.serverIn.Write([]byte(cmd))
			if !strings.HasSuffix(cmd, "\n") {
				self.serverIn.Write([]byte{'\n'})
			}

		case run := <-signal:
			for !run {
				run = <-signal
			}

		case line := <-in:
			self.serverIn.Write([]byte(line))
			if !strings.HasSuffix(line, "\n") {
				self.serverIn.Write([]byte{'\n'})
			}
		}
	}
}
