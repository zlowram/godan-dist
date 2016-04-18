package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"

	_ "github.com/go-sql-driver/mysql"
	"github.com/husobee/vestigo"
	"github.com/jroimartin/monmq"
	"github.com/jroimartin/orujo"
	olog "github.com/jroimartin/orujo/log"
	"github.com/jroimartin/rpcmq"
)

type server struct {
	config     Config
	logger     *log.Logger
	client     *rpcmq.Client
	supervisor *monmq.Supervisor
	database   *sql.DB
}

func newServer(cfg Config) *server {
	s := &server{
		config: cfg,
		logger: log.New(os.Stdout, "[godan] ", log.LstdFlags),
	}

	return s
}

func (s *server) start() error {
	s.client = rpcmq.NewClient("amqp://"+s.config.Rpcmq.Host+":"+s.config.Rpcmq.Port, s.config.Rpcmq.MsgQueue, s.config.Rpcmq.ReplyQueue, s.config.Rpcmq.Exchange, s.config.Rpcmq.ExchangeType)
	s.client.DeliveryMode = rpcmq.Transient
	err := s.client.Init()
	if err != nil {
		log.Fatalf("Init rpcmq: %v", err)
	}
	defer s.client.Shutdown()

	s.supervisor = monmq.NewSupervisor("amqp://"+s.config.Monmq.Host+":"+s.config.Monmq.Port, s.config.Monmq.ReplyQueue, s.config.Monmq.Exchange)
	if err := s.supervisor.Init(); err != nil {
		log.Fatalf("Init monmq: %v", err)
	}
	defer s.supervisor.Shutdown()

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4,utf8", s.config.DB.Username, s.config.DB.Password, s.config.DB.Host, s.config.DB.Port, s.config.DB.Name)
	s.database, err = sql.Open("mysql", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer s.database.Close()

	_, err = s.database.Exec("CREATE TABLE IF NOT EXISTS banners (ip INT UNSIGNED, port INT UNSIGNED, service VARCHAR(50), content MEDIUMTEXT)")
	if err != nil {
		log.Fatal(err)
	}

	m := vestigo.NewRouter()
	logHandler := olog.NewLogHandler(s.logger, logLine)

	m.Post("/tasks", orujo.NewPipe(
		http.HandlerFunc(s.tasksHandler),
		orujo.M(logHandler)).ServeHTTP,
	)
	m.Get("/status", orujo.NewPipe(
		http.HandlerFunc(s.getStatusHandler),
		orujo.M(logHandler)).ServeHTTP,
	)
	m.Post("/status", orujo.NewPipe(
		http.HandlerFunc(s.setStatusHandler),
		orujo.M(logHandler)).ServeHTTP,
	)
	m.Get("/ips/:ip", orujo.NewPipe(
		http.HandlerFunc(s.queryHandler),
		orujo.M(logHandler)).ServeHTTP,
	)
	m.Get("/ips", orujo.NewPipe(
		http.HandlerFunc(s.queryHandler),
		orujo.M(logHandler)).ServeHTTP,
	)

	fmt.Println("Listening on " + s.config.Local.Host + ":" + s.config.Local.Port + "...")
	log.Fatalln(http.ListenAndServe(s.config.Local.Host+":"+s.config.Local.Port, m))

	return nil
}

const (
	logLine = `{{.Req.RemoteAddr}} - {{.Req.Method}} {{.Req.RequestURI}}
		{{range  $err := .Errors}}  Err: {{$err}}
		{{end}}`
	errLine = `{"error":"{{.}}"}`
)
