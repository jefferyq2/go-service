// +build windows

package master

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	stateFd       = 5
	listenFdStart = 6
)

var (
	listenFdCount int = 1
	confPath      string
	sockType      string
	services      string
	privilege   = false
	verbose     = false
	chrootOn    = false
)

type PreJailFunc func()
type InitFunc func()
type ExitFunc func()

var (
	preJailHandler PreJailFunc = nil
	initHandler    InitFunc    = nil
	exitHandler    ExitFunc    = nil
	doneChan       chan bool   = make(chan bool)
	connCount      int         = 0
	connMutex      sync.RWMutex
	stopping       bool = false
	prepareCalled  bool = false
)

// from configure file of the app
var (
	MasterService  string
	MasterLogPath  string
	MasterOwner    string
	MasterArgs     string
	AppRootDir     string
	AppUseLimit    int    = 0
	AppIdleLimit   int    = 0
	AppQuickAbort  bool   = false
	AppWaitLimit   int    = 10
	AppAccessAllow string = "all"
	Appthreads     int    = 0
)

// from command args
var (
	MasterConfigure    string
	MasterServiceName  string
	MasterServiceType  string
	MasterVerbose      bool
	MasterUnprivileged bool
	//	MasterChroot       bool
	MasterSocketCount int = 1
	Alone             bool
)

// set the max opened file handles for current process which let
// the process can handle more connections.
func setOpenMax() {
}

// init the command args come from acl_master; the application should call
// flag.Parse() in its main function!
func initFlags() {
	flag.StringVar(&MasterConfigure, "f", "", "app configure file")
	flag.StringVar(&MasterServiceName, "n", "", "app service name")
	flag.StringVar(&MasterServiceType, "t", "sock", "app service type")
	flag.BoolVar(&Alone, "alone", false, "stand alone running")
	flag.BoolVar(&MasterVerbose, "v", false, "app verbose")
	flag.BoolVar(&MasterUnprivileged, "u", false, "app unprivileged")
	//	flag.BoolVar(&MasterChroot, "c", false, "app chroot")
	flag.IntVar(&MasterSocketCount, "s", 1, "listen fd count")
}

func init() {
	initFlags()
	setOpenMax()
}

func parseArgs() {
	var n = len(os.Args)
	for i := 0; i < n; i++ {
		switch os.Args[i] {
		case "-s":
			i++
			if i < n {
				listenFdCount, _ = strconv.Atoi(os.Args[i])
				if listenFdCount <= 0 {
					listenFdCount = 1
				}
			}
		case "-f":
			i++
			if i < n {
				confPath = os.Args[i]
			}
		case "-t":
			i++
			if i < n {
				sockType = os.Args[i]
			}
		case "-n":
			i++
			if i < n {
				services = os.Args[i]
			}
		case "-u":
			privilege = true
		case "-v":
			verbose = true
		case "-c":
			chrootOn = true
		}
	}

	log.Printf("ListenFdCount=%d, sockType=%s, services=%s",
		listenFdCount, sockType, services)
}

// Prepare this function can be called automatically in net_service.go or
// web_service.go to load configure, and it can also be canned in application's
// main function
func Prepare() {
	if prepareCalled {
		return
	} else {
		prepareCalled = true
	}

	parseArgs()

	conf := new(Config)
	conf.InitConfig(confPath)

	MasterLogPath = conf.GetString("master_log")
	if len(MasterLogPath) > 0 {
		f, err := os.OpenFile(MasterLogPath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			fmt.Printf("OpenFile %s error %s", MasterLogPath, err)
		} else {
			log.SetOutput(f)
			//log.SetOutput(io.MultiWriter(os.Stderr, f))
		}
	}

	MasterService = conf.GetString("master_service")
	MasterOwner = conf.GetString("master_owner")
	MasterArgs = conf.GetString("master_args")

	AppRootDir = conf.GetString("app_queue_dir")
	AppUseLimit = conf.GetInt("app_use_limit")
	AppIdleLimit = conf.GetInt("app_idle_limit")
	AppQuickAbort = conf.GetBool("app_quick_abort")
	AppWaitLimit = conf.GetInt("app_wait_limit")
	AppAccessAllow = conf.GetString("app_access_allow")
	Appthreads = conf.GetInt("app_threads")
	if Appthreads > 0 {
		runtime.GOMAXPROCS(Appthreads)
	}

	log.Printf("Args: %s, AppAccessAllow: %s\r\n", MasterArgs, AppAccessAllow)
}

func chroot() {
	if len(MasterArgs) == 0 || !privilege || len(MasterOwner) == 0 {
		return
	}

	_, err := user.Lookup(MasterOwner)
	if err != nil {
		log.Printf("Lookup %s error %s", MasterOwner, err)
	}
}

// GetListenersByAddrs In run alone mode, the application should give the listening addrs
// and call this function to listen the given addrs
func GetListenersByAddrs(addrs string) ([]net.Listener, error) {
	if len(addrs) == 0 {
		log.Println("No valid addrs for listening")
		return nil, errors.New("No valid addrs for listening")
	}

	// split addrs like "xxx.xxx.xxx.xxx:port; xxx.xxx.xxx.xxx:port"

	addrs = strings.Replace(addrs, " ", "", -1)
	addrs = strings.Replace(addrs, ",", ";", -1)
	tokens := strings.Split(addrs, ";")

	listeners := []net.Listener(nil)
	for _, addr := range tokens {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			listeners = append(listeners, ln)
			log.Printf("Listen %s ok", addr)
			continue
		}

		log.Println("Listen", addr, "error:", err)
	}
	if len(listeners) == 0 {
		log.Println("No listeners were createdd")
		return nil, errors.New("No listeners were created")
	}
	return listeners, nil
}

// GetListeners in acl_master daemon running mode, this function will be called to
// init the listener handles.
func GetListeners() ([]net.Listener, error) {
	listeners := []net.Listener(nil)
	for fd := listenFdStart; fd < listenFdStart+listenFdCount; fd++ {
		file := os.NewFile(uintptr(fd), "open one listenfd")
		if file == nil {
			log.Printf("os.NewFile failed for fd=%d", fd)
			continue
		}

		ln, err := net.FileListener(file)

		// fd will be duped in FileListener, so we should close it
		// after the listener is created
		_ = file.Close()

		if err == nil {
			listeners = append(listeners, ln)
			log.Printf("add fd %d ok\r\n", fd)
			continue
		}
		log.Println(fmt.Sprintf("Create FileListener error=\"%s\", fd=%d", err, fd))
	}

	if len(listeners) == 0 {
		log.Println("No listener created!")
		return nil, errors.New("No listener created")
	} else {
		log.Printf("Listeners's len=%d", len(listeners))
	}
	return listeners, nil
}

func ServiceInit(addrs string, stopHandler func(bool)) ([]net.Listener, error) {
	Prepare()

	if preJailHandler != nil {
		preJailHandler()
	}

	chroot()

	if initHandler != nil {
		initHandler()
	}

	// if addrs not empty, alone mode will be used, or daemon mode be used

	var listeners []net.Listener
	var daemonMode bool
	
	if len(addrs) > 0 {
		var err error
		listeners, err = GetListenersByAddrs(addrs)
		if err != nil {
			return nil, err
		}
		daemonMode = false
	} else {
		var err error
		listeners, err = GetListeners()
		if err != nil {
			log.Println("GetListeners error", err)
			return nil, err
		}
		daemonMode = true
	}

	if len(listeners) == 0 {
		log.Println("No listener available!")
		return nil, errors.New("No listener available")
	}

	// In daemon mode, the backend monitor fiber will be created for
	// monitoring the status with the acl_master framework. If disconnected
	// from acl_master, the current child process will exit.
	if daemonMode {
		go monitorMaster(listeners, nil, stopHandler)
	}
	return listeners, nil
}

// monitorMaster monitor the PIPE IPC between the current process and acl_master,
// when acl_master close thePIPE, the current process should exit after
// which has handled all its tasks
func monitorMaster(listeners []net.Listener,
	onStopHandler func(), stopHandler func(bool)) {

	file := os.NewFile(uintptr(stateFd), "")
	conn, err := net.FileConn(file)
	if err != nil {
		panic(fmt.Sprintf("FileConn error=%s", err))
	}

	log.Println("Waiting for master exiting ...")

	buf := make([]byte, 1024)
	_, err = conn.Read(buf)
	if err != nil {
		log.Println("Disconnected from master", err)
	}

	stopping = true

	if onStopHandler != nil {
		onStopHandler()
	} else {
		// XXX: force stopping listen again
		for _, ln := range listeners {
			log.Println("Closing one listener ", ln.Addr())
			_ = ln.Close()
		}
	}

	var n, i int
	n = 0
	i = 0

	for {
		connMutex.RLock()
		if connCount <= 0 {
			connMutex.RUnlock()
			break
		}

		n = connCount
		connMutex.RUnlock()
		time.Sleep(time.Second) // sleep 1 second
		i++
		log.Printf("Exiting, clients=%d, sleep=%d seconds\r\n", n, i)
		if AppWaitLimit > 0 && i >= AppWaitLimit {
			log.Printf("Waiting too long >= %d", AppWaitLimit)
			break
		}
	}

	log.Println("Master disconnected, exiting now")

	if stopHandler != nil {
		stopHandler(true)
	}
}

func connCountInc() {
	connMutex.Lock()
	connCount++
	connMutex.Unlock()
}

func connCountDec() {
	connMutex.Lock()
	connCount--
	connMutex.Unlock()
}

func connCountCur() int {
	connMutex.RLock()
	n := connCount
	connMutex.RUnlock()
	return n
}

func OnPreJail(handler PreJailFunc) {
	preJailHandler = handler
}

func OnInit(handler InitFunc) {
	initHandler = handler
}

func OnExit(handler ExitFunc) {
	exitHandler = handler
}
