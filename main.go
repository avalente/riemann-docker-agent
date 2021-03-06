package main

import (
	"bytes"
	"fmt"
	"log"
	"math"
	"net/url"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/amir/raidman"
	"github.com/samalba/dockerclient"
	"gopkg.in/alecthomas/kingpin.v1"
)

const VERSION = "1.0.0"

type EventConfig struct {
	Host        string
	Service     *template.Template
	Ttl         float64
	Description *template.Template
	Tags        []*template.Template
	State       *template.Template
	Metric      float64
	Attributes  map[string]*template.Template
}

type DockerEventInfo struct {
	Host          string
	Time          int64
	ContainerId   string
	Status        string
	Image         string
	Name          string // container name or id if name is unavailable
	ContainerInfo *dockerclient.ContainerInfo
}

func connectToRiemann(riemannUrl string) *raidman.Client {
	for i := 0; i < 10; i++ {
		url, err := url.Parse(riemannUrl)
		if err != nil {
			log.Fatalf("Failed to parse Riemann URL %s: %s\n", riemannUrl, err)
		}

		conn, err := raidman.Dial(url.Scheme, url.Host)
		if err == nil {
			log.Printf("connected to riemann on %s", riemannUrl)
			return conn
		}

		w := math.Pow(2, float64(i))
		log.Printf("ERROR: can't connect to riemann: %v - waiting %v seconds", err, w)
		timerChan := time.After(time.Duration(w) * time.Second)

		select {
		case <-timerChan:
			continue
		}

	}

	panic("can't connect to riemann")
}

func connectToDocker(dockerHost string) *dockerclient.DockerClient {
	docker, err := dockerclient.NewDockerClient(dockerHost, nil)
	if err != nil {
		log.Fatalf("Failed to connect to Docker host %s: %s\n", dockerHost, err)
	}

	version, err := docker.Version()
	if err != nil {
		log.Fatalf("Failed to fetch docker daemon version: %s", err)
	} else {
		log.Printf("Connected to docker @ %s (version %s)", dockerHost, version.Version)
	}

	return docker
}

func main() {
	hostName, _ := os.Hostname()

	var defaultDockerHost = os.Getenv("DOCKER_HOST")
	if defaultDockerHost == "" {
		defaultDockerHost = "unix:///var/run/docker.sock"
	}

	var (
		riemannUrl = kingpin.Flag("riemann-url", "Riemann URL").Default("tcp://localhost:5555").String()
		dockerHost = kingpin.Flag("docker-host", "Docker host").Default(defaultDockerHost).String()

		verbose = kingpin.Flag("verbose", "Verbose").Short('v').Bool()

		host        = kingpin.Flag("host", "Event Host").Short('h').Default(hostName).String()
		service     = kingpin.Flag("service", "Event service").Short('s').Default("docker {{.Name}} {{.Status}}").String()
		ttl         = kingpin.Flag("ttl", "Event TTL").Default("60").Float()
		description = kingpin.Flag("description", "Event description").Short('d').Default("container {{.Name}} {{.Status}}").String()
		tags        = kingpin.Flag("tag", "Event tag (can be specified multiple times)").Short('t').Strings()
		state       = kingpin.Flag("state", "Event state").Default("{{.Status}}").String()
		metric      = kingpin.Flag("metric", "Event metric").Short('m').Default("0").Float()
		attributes  = kingpin.Flag("attribute", "Event attributes").Short('a').StringMap()

		hbService     = kingpin.Flag("hb-service", "Heartbeat service").Default("riemann-docker-agent").String()
		hbTtl         = kingpin.Flag("hb-ttl", "Heartbeat TTL").Default("60").Float()
		hbDescription = kingpin.Flag("hb-description", "Heartbeat description").Default("docker-agent is alive").String()
		hbTags        = kingpin.Flag("hb-tag", "Heartbeat tag (can be specified multiple times)").Strings()
		hbState       = kingpin.Flag("hb-state", "Heartbeat state").Default("ok").String()
		hbMetric      = kingpin.Flag("hb-metric", "Heartbeat metric").Default("0").Float()
		hbAttributes  = kingpin.Flag("hb-attribute", "Heartbeat attributes").StringMap()
	)

	kingpin.Version(VERSION)
	kingpin.Parse()

	ec := EventConfig{
		Host:        *host,
		Service:     getTemplate("service", service),
		Ttl:         *ttl,
		Description: getTemplate("description", description),
		State:       getTemplate("state", state),
		Metric:      *metric,
		Attributes:  make(map[string]*template.Template),
	}

	for i, value := range *tags {
		ec.Tags = append(ec.Tags, getTemplate(fmt.Sprintf("tag %d", i+1), &value))
	}

	for key, value := range *attributes {
		ec.Attributes[key] = getTemplate(fmt.Sprintf("attribute '%s'", key), &value)
	}

	// heartbeat event
	hb := raidman.Event{
		Host:        *host,
		Description: *hbDescription,
		Service:     *hbService,
		Metric:      *hbMetric,
		State:       *hbState,
		Tags:        *hbTags,
		Ttl:         float32(*hbTtl),
		Attributes:  *hbAttributes,
	}

	dockerClient := connectToDocker(*dockerHost)

	waitForEvents(*riemannUrl, dockerClient, &ec, &hb, *verbose)
}

func getTemplate(name string, value *string) *template.Template {
	tpl, err := template.New(name).Parse(*value)
	if err != nil {
		log.Fatalf("bad value for %s (%s)\n", name, *value)
	}
	return tpl
}

func execTemplate(tpl *template.Template, info *DockerEventInfo) string {
	var doc bytes.Buffer
	if err := tpl.Execute(&doc, info); err != nil {
		log.Printf("%s\n", err.Error())
		return ""
	}
	return doc.String()
}

func dockerEventCallback(event *dockerclient.Event, errChan chan error, args ...interface{}) {
	dockerClient := args[0].(*dockerclient.DockerClient)
	evChan := args[1].(chan *DockerEventInfo)
	verbose := args[2].(bool)

	if verbose {
		log.Printf("Received event %+v", event)
	}

	eventInfo := DockerEventInfo{
		Time:          event.Time,
		ContainerId:   event.Id,
		Status:        event.Status,
		Image:         event.From,
		ContainerInfo: &dockerclient.ContainerInfo{},
	}

	if event.Status != "destroy" {
		info, err := dockerClient.InspectContainer(event.Id)
		if err != nil {
			log.Printf("error inspecting container %s: %s\n", event.Id, err)
		} else {
			eventInfo.ContainerInfo = info
		}
	}

	if eventInfo.ContainerInfo.Name != "" {
		eventInfo.Name = strings.Replace(eventInfo.ContainerInfo.Name, "/", "", 1)
	} else {
		eventInfo.Name = event.Id
	}

	evChan <- &eventInfo
}

func eventTransformer(inChan chan *DockerEventInfo, cfg *EventConfig, outChan chan *raidman.Event) {
	for ev := range inChan {
		if ev == nil {
			return
		}

		outChan <- getRiemannEvent(ev, cfg)
	}
}

func riemannSender(riemannUrl string, evChan chan *raidman.Event, verbose bool) {
	var client *raidman.Client = nil

	for ev := range evChan {
		if ev == nil {
			return
		}

		if client == nil {
			client = connectToRiemann(riemannUrl)
		}

		if !sendEvent(client, ev, verbose) {
			client = nil
			// re-enqueue message
			evChan <- ev
		}
	}
}

func getRiemannEvent(info *DockerEventInfo, cfg *EventConfig) *raidman.Event {
	// Set the host, it could be used by templates
	info.Host = cfg.Host

	tags := make([]string, len(cfg.Tags))
	for i, tag := range cfg.Tags {
		tags[i] = execTemplate(tag, info)
	}

	attributes := make(map[string]string)
	for k, v := range cfg.Attributes {
		attributes[k] = execTemplate(v, info)
	}

	ev := raidman.Event{
		Host:        info.Host,
		Time:        info.Time,
		Description: execTemplate(cfg.Description, info),
		Service:     execTemplate(cfg.Service, info),
		Metric:      cfg.Metric,
		State:       execTemplate(cfg.State, info),
		Tags:        tags,
		Attributes:  attributes,
	}

	return &ev
}

func sendEvent(client *raidman.Client, ev *raidman.Event, verbose bool) bool {
	if verbose {
		log.Printf("Sending event %+v\n", ev)
	}

	if err := client.Send(ev); err != nil {
		log.Printf("Can't send metrics to riemann: %s", err)
		return false
	} else {
		return true
	}
}

func heartbeatGenerator(outChan chan *raidman.Event, ev *raidman.Event, interval time.Duration) {
	ticker := time.NewTicker(interval)
	for t := range ticker.C {
		ev.Time = t.Unix()
		outChan <- ev
	}
}

func waitForEvents(riemannUrl string, dockerClient *dockerclient.DockerClient, eventConfig *EventConfig, heartbeat *raidman.Event, verbose bool) {
	dockerEventsChan := make(chan *DockerEventInfo, 10000)
	riemannEventsChan := make(chan *raidman.Event, 10000)

	// heartbeat event generator
	if heartbeat.Service != "" {
		interval := time.Duration(heartbeat.Ttl/2) * time.Second
		log.Printf("sending heartbeat every %s", interval)
		go heartbeatGenerator(riemannEventsChan, heartbeat, interval)
	} else {
		log.Printf("heartbeat disabled")
	}

	// start docker to riemann events consumer
	go eventTransformer(dockerEventsChan, eventConfig, riemannEventsChan)

	// start riemann event sender
	go riemannSender(riemannUrl, riemannEventsChan, verbose)

	// start docker events monitor
	dockerClient.StartMonitorEvents(dockerEventCallback, nil, dockerClient, dockerEventsChan, verbose)
	select {}
}
