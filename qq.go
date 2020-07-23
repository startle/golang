package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gocolly/colly"
	"github.com/spf13/viper"
)

var (
	ResponseLog *log.Logger
	ErrorLog    *log.Logger
	MonitorLog  *log.Logger

	CurId        int
	BId          int
	EId          int
	ThreadCount  int
	MonitorCount int
	Logdir       string
	ConfFile     string
	mu           sync.Mutex
)

func newLog(filename string, stdout bool, flag int) *log.Logger {
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Println("Failed to open error log file:", err)
	}
	out := io.Writer(file)
	if stdout {
		out = io.MultiWriter(file, os.Stdout)
	}
	return log.New(out, "", flag)
}
func dirExist(path string) bool {
	s, err := os.Stat(path)
	if err != nil {
		return false
	}
	return s.IsDir()
}
func initLog() {
	if !dirExist(Logdir) {
		os.MkdirAll(Logdir, os.ModePerm)
	}
	ResponseLog = newLog(Logdir+"/responsed.log", false, 0)
	ErrorLog = newLog(Logdir+"/error.log", false, log.Ltime)
	MonitorLog = newLog(Logdir+"/monitor.log", true, log.Ltime)
}
func initConf() {
	viper.SetConfigName(ConfFile)
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	err := viper.ReadInConfig()
	if err != nil {
		fmt.Printf("config file error: %s\n", err)
		os.Exit(1)
	}
	ThreadCount = viper.GetInt("threadCount")
	MonitorCount = viper.GetInt("monitorCount")
	BId = viper.GetInt("beginId")
	EId = viper.GetInt("endId")
	Logdir = viper.GetString("logdir")
	viper.WatchConfig()
	viper.OnConfigChange(func(e fsnotify.Event) {
		EId = viper.GetInt("endId")
		fmt.Println("配置发生变更：", e.Name, EId)
	})

	fmt.Printf("[%d, %d] -> %s\n", BId, EId, Logdir)
}

func nextId() int {
	mu.Lock()
	CurId++
	var nId int
	if CurId < EId {
		nId = CurId
	} else {
		nId = 0
	}
	mu.Unlock()
	return nId
}
func main() {
	if len(os.Args) > 1 {
		ConfFile = os.Args[1]
	} else {
		ConfFile = "conf.yaml"
	}
	initConf()
	initLog()
	c := colly.NewCollector(
		colly.Async(true),
		colly.UserAgent("Mozilla/5.0 (Windows NT 6.1; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/80.0.3987.132 Safari/537.36"),
		colly.AllowedDomains("y.qq.com"),
	)
	c.Limit(&colly.LimitRule{DomainGlob: "y.qq.com", Parallelism: ThreadCount})

	c.OnHTML("div.data__singer a.data__singer_txt.js_user", func(e *colly.HTMLElement) {
		e.Response.Ctx.Put("owner", e.Attr("title"))
	})
	c.OnHTML("h1[class=data__name_txt]", func(e *colly.HTMLElement) {
		e.Response.Ctx.Put("title", e.Attr("title"))
	})

	c.OnScraped(func(r *colly.Response) {
		id, _ := strconv.Atoi(r.Ctx.Get("id"))
		owner := r.Ctx.Get("owner")
		title := r.Ctx.Get("title")
		ResponseLog.Println(id, owner, title)
	})
	count := int32(0)
	BeginTime := time.Now().Unix()
	LastTime := BeginTime - 1
	c.OnResponse(func(r *colly.Response) {
		id, _ := strconv.Atoi(r.Ctx.Get("id"))
		nextId := nextId()
		if nextId > 0 {
			nurl := "https://y.qq.com/n/yqq/playlist/" + strconv.Itoa(nextId) + ".html"
			c.Visit(nurl)
		}
		count_ := int(atomic.AddInt32(&count, 1))
		if count_%MonitorCount == 0 {
			now := time.Now().Unix()
			pasttime := now - BeginTime
			pasttimeStr := fmt.Sprintf("%2d:%2d:%2d", pasttime/3600, pasttime/60%60, pasttime%60)
			MonitorLog.Printf("tps:%d\ttime:%s\tcount:%d\tid:%d\n", MonitorCount/int(now-LastTime), pasttimeStr, count, id)
			LastTime = now - 1
		}
	})

	c.OnError(func(r *colly.Response, err error) {
		ErrorLog.Println(r.Request.URL.String(), err)
		r.Request.Retry()
	})

	c.OnRequest(func(r *colly.Request) {
		r.Headers.Set("Accept-Encoding", "gzip, deflate")
		id := r.URL.String()[32:42]
		r.Ctx.Put("id", id)
	})

	CurId = BId - 1
	for i := 0; i < ThreadCount*10; i++ {
		url := "https://y.qq.com/n/yqq/playlist/" + strconv.Itoa(nextId()) + ".html"
		c.Visit(url)
	}
	c.Wait()
}
