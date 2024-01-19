package engine

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hypebeast/go-osc/osc"
	nats "github.com/nats-io/nats.go"
	"github.com/vizicist/portmidi"
)

const debug bool = false

// Router takes events and routes them
type Router struct {
	regionLetters string
	reactors      map[string]*Reactor
	inputs        []*osc.Client
	OSCInput      chan OSCEvent
	MIDIInput     chan portmidi.Event

	cursorCallbacks      []GestureDeviceCallbackFunc
	killme               bool // true if Router should be stopped
	lastClick            Clicks
	control              chan Command
	time                 time.Time
	time0                time.Time
	killPlayback         bool
	recordingOn          bool
	recordingFile        *os.File
	recordingBegun       time.Time
	resolumeClient       *osc.Client
	guiClient            *osc.Client
	plogueClient         *osc.Client
	publishGesture       bool
	publishMIDI          bool
	myHostname           string
	generateVisuals      bool
	generateSound        bool
	regionForMorph       map[string]string // for all known Morph serial#'s
	regionAssignedToNUID map[string]string
	regionAssignedMutex  sync.RWMutex // covers both regionForMorph and regionAssignedToNUID
	eventMutex           sync.RWMutex
}

// OSCEvent is an OSC message
type OSCEvent struct {
	Msg    *osc.Message
	Source string
}

// Command is sent on the control channel of the Router
type Command struct {
	Action string // e.g. "addmidi"
	Arg    interface{}
}

// DeviceGesture purpose is to know when it
// hasn't been seen for a while,
// in order to generate an UP event
type DeviceGesture struct {
	lastTouch time.Time
	downed    bool // true if cursor down event has been generated
}

// APIEvent is an API invocation
type APIEvent struct {
	apiType string // sound, visual, effect, global
	pad     string
	method  string
	args    []string
}

// PlaybackEvent is a time-tagged cursor or API event
type PlaybackEvent struct {
	time      float64
	eventType string
	pad       string
	method    string
	args      map[string]string
	rawargs   string
}

var onceRouter sync.Once
var oneRouter Router

// APIExecutorFunc xxx
type APIExecutorFunc func(api string, nuid string, rawargs string) (result interface{}, err error)

// CallAPI calls an API in the Montage Freeframe plugin running in Resolume
func CallAPI(method string, args []string) {
	log.Printf("CallAPI method=%s", method)
}

func recordingsFile(nm string) string {
	return ConfigFilePath(filepath.Join("recordings", nm))
}

// TheRouter returns a pointer to the one-and-only Router
func TheRouter() *Router {
	onceRouter.Do(func() {

		oneRouter.regionLetters = "ABCD"

		err := LoadParamEnums()
		if err != nil {
			log.Printf("LoadParamEnums: err=%s\n", err)
			// might be fatal, but try to continue
		}
		err = LoadParamDefs()
		if err != nil {
			log.Printf("LoadParamDefs: err=%s\n", err)
			// might be fatal, but try to continue
		}
		err = LoadResolumeJSON()
		if err != nil {
			log.Printf("LoadResolumeJSON: err=%s\n", err)
			// might be fatal, but try to continue
		}

		oneRouter.cursorCallbacks = make([]GestureDeviceCallbackFunc, 0)
		oneRouter.reactors = make(map[string]*Reactor)
		oneRouter.regionForMorph = make(map[string]string)
		oneRouter.regionAssignedToNUID = make(map[string]string)

		oneRouter.resolumeClient = osc.NewClient("127.0.0.1", 7000)
		oneRouter.guiClient = osc.NewClient("127.0.0.1", 3943)
		oneRouter.plogueClient = osc.NewClient("127.0.0.1", 3210)

		for i, c := range oneRouter.regionLetters {
			resolumeLayer := 1 + i
			resolumePort := 3334 + i
			ch := string(c)
			freeframeClient := osc.NewClient("127.0.0.1", resolumePort)
			oneRouter.reactors[ch] = NewReactor(ch, resolumeLayer, freeframeClient, oneRouter.resolumeClient, oneRouter.guiClient)
		}

		oneRouter.OSCInput = make(chan OSCEvent)
		oneRouter.MIDIInput = make(chan portmidi.Event)
		oneRouter.recordingOn = false

		oneRouter.myHostname = ConfigValue("hostname")
		if oneRouter.myHostname == "" {
			hostname, err := os.Hostname()
			if err != nil {
				log.Printf("os.Hostname: err=%s\n", err)
				hostname = "unknown"
			}
			oneRouter.myHostname = hostname
		}

		oneRouter.publishGesture = ConfigBool("publishcursor")
		oneRouter.publishMIDI = ConfigBool("publishmidi")
		oneRouter.generateVisuals = ConfigBool("generatevisuals")
		oneRouter.generateSound = ConfigBool("generatesound")

		go oneRouter.notifyGUI("restart")
	})
	return &oneRouter
}

// StartOSCListener xxx
func StartOSCListener(source string) {

	r := TheRouter()
	handler := r.OSCInput

	d := osc.NewStandardDispatcher()

	err := d.AddMsgHandler("*", func(msg *osc.Message) {
		handler <- OSCEvent{Msg: msg, Source: source}
	})
	if err != nil {
		log.Printf("ERROR! %s\n", err.Error())
	}

	server := &osc.Server{
		Addr:       source,
		Dispatcher: d,
	}
	server.ListenAndServe()
}

// StartNATSListener xxx
func StartNATSListener() {

	StartVizNats()

	// Hand all NATS messages to HandleAPI
	router := TheRouter()

	log.Printf("StartNATS: Subscribing to %s\n", MontageAPISubject)
	TheVizNats.Subscribe(MontageAPISubject, func(msg *nats.Msg) {
		data := string(msg.Data)
		response := router.HandleAPIInput(router.ExecuteAPI, data)
		msg.Respond([]byte(response))
	})

	log.Printf("StartNATS: subscribing to %s\n", MontageEventSubject)
	TheVizNats.Subscribe(MontageEventSubject, func(msg *nats.Msg) {
		data := string(msg.Data)
		args, err := StringMap(data)
		if err != nil {
			log.Printf("HandleSubscribedEvent: err=%s\n", err)
		}
		err = router.HandleSubscribedEventArgs(args)
		if err != nil {
			log.Printf("HandleSubscribedEvent: err=%s\n", err)
		}
	})
}

// TimeString returns time and clicks
func TimeString() string {
	r := TheRouter()
	sofar := r.time.Sub(r.time0)
	click := Seconds2Clicks(sofar.Seconds())
	return fmt.Sprintf("sofar=%f click=%d", sofar.Seconds(), click)

}

// StartRealtime runs the looper and never returns
func StartRealtime() {

	log.Println("StartRealtime begins")

	r := TheRouter()

	// Wake up every 2 milliseconds and check looper events
	tick := time.NewTicker(2 * time.Millisecond)
	r.time0 = <-tick.C

	var lastPrintedClick Clicks

	// By reading from tick.C, we wake up every 2 milliseconds
	for now := range tick.C {
		// log.Printf("Realtime loop now=%v\n", time.Now())
		r.time = now
		sofar := now.Sub(r.time0)
		secs := sofar.Seconds()
		newclick := Seconds2Clicks(secs)
		CurrentMilli = int(secs * 1000.0)

		if newclick > currentClick {
			// log.Printf("ADVANCING CLICK now=%v click=%d\n", time.Now(), newclick)
			r.advanceClickTo(currentClick)
			currentClick = newclick
		}

		var everySoOften = oneBeat * 4
		if (currentClick%everySoOften) == 0 && currentClick != lastPrintedClick {
			if debug {
				log.Printf("currentClick=%d  unix=%d:%d\n", currentClick, time.Now().Unix(), time.Now().UnixNano())
			}
			lastPrintedClick = currentClick
		}

		select {
		case cmd := <-r.control:
			_ = cmd
			log.Println("Realtime got command on control channel: ", cmd)
		default:
		}
	}
	log.Println("StartRealtime ends")
}

// StartMIDI listens for MIDI events and sends them to the MIDIInput chan
func StartMIDI() {
	r := TheRouter()
	midiinput := ConfigValue("midiinput")
	if midiinput == "" {
		return
	}
	words := strings.Split(midiinput, ",")
	inputs := make(map[string]*midiInput)
	for _, input := range words {
		i := MIDI.getInput(input)
		if i == nil {
			log.Printf("StartMIDI: There is no input named %s\n", input)
		} else {
			inputs[input] = i
		}
	}
	log.Printf("Successfully opened MIDI input device %s\n", midiinput)
	for {
		for nm, input := range inputs {
			hasinput, err := input.Poll()
			if err != nil {
				log.Printf("StartMIDI: Poll of input=%s err=%v\n", nm, err)
			} else if hasinput {
				event, err := input.ReadEvent()
				if err != nil {
					log.Printf("StartMIDI: ReadEvent of input=%s err=%s\n", nm, err)
					// we may have lost some NOTEOFF's so reset our count
					// r.MIDINumDown = 0
				} else {
					if DebugUtil.MIDI {
						log.Printf("StartMIDI: input=%s event=%+v\n", nm, event)
					}
					r.MIDIInput <- event
				}
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// ListenForLocalDeviceInputsForever listens for local device inputs (OSC, MIDI)
func ListenForLocalDeviceInputsForever() {
	r := TheRouter()
	for r.killme == false {
		select {
		case msg := <-r.OSCInput:
			r.HandleOSCInput(msg)
		case event := <-r.MIDIInput:
			if r.publishMIDI {
				me := MIDIDeviceEvent{
					Timestamp: int64(event.Timestamp),
					Status:    event.Status,
					Data1:     event.Data1,
					Data2:     event.Data2,
				}
				err := PublishMIDIDeviceEvent(me)
				if err != nil {
					log.Printf("Router.HandleDevieMIDIInput: me=%+v err=%s\n", me, err)
				}
			}
			// XXX - All Pads??  I guess
			for _, reactor := range r.reactors {
				reactor.HandleMIDIDeviceInput(event)
			}
		default:
			// log.Printf("Sleeping 1 ms - now=%v\n", time.Now())
			time.Sleep(time.Millisecond)
		}
	}
	log.Printf("Router is being killed\n")
}

// HandleSubscribedEventArgs xxx
func (r *Router) HandleSubscribedEventArgs(args map[string]string) error {

	r.eventMutex.Lock()
	defer r.eventMutex.Unlock()

	// All Events should have nuid and event values

	nuid, err := NeedStringArg("nuid", "HandleSubscribeEvent", args)
	if err != nil {
		return err
	}

	/*
		// Ignore anything from myself
		if nuid == MyNUID() {
			return nil
		}
	*/

	event, err := NeedStringArg("event", "HandleSubscribeEvent", args)
	if err != nil {
		return err
	}

	// If no "region" argument, use one assigned to NUID

	region := OptionalStringArg("region", args, "")
	if region == "" {
		region = r.getRegionForNUID(nuid)
	} else {
		// Remove it from the args
		delete(args, "region")
	}

	reactor, ok := r.reactors[region]
	if !ok {
		return fmt.Errorf("there is no region named %s", region)
	}

	eventWords := strings.SplitN(event, "_", 2)
	subEvent := ""
	mainEvent := event
	if len(eventWords) > 1 {
		mainEvent = eventWords[0]
		subEvent = eventWords[1]
	}

	switch mainEvent {

	case "cursor":

		// If we're publishing cursor events, we ignore ones from ourself
		if r.publishGesture && nuid == MyNUID() {
			return nil
		}

		id := OptionalStringArg("id", args, "UnspecifiedID")

		switch subEvent {
		case "down":
		case "drag":
		case "up":
		default:
			return fmt.Errorf("handleSubscribedEvent: Unexpected cursor event type: %s", subEvent)
		}

		x, y, z, err := GetXYZ("handleSubscribedEvent", args)
		if err != nil {
			return err
		}

		ce := GestureDeviceEvent{
			NUID:       nuid,
			Region:     region,
			ID:         id,
			Timestamp:  int64(CurrentMilli),
			DownDragUp: subEvent,
			X:          x,
			Y:          y,
			Z:          z,
			Area:       0.0,
		}

		if r.publishGesture {
			err := PublishGestureDeviceEvent(ce)
			if err != nil {
				log.Printf("Router.routeGestureDeviceEvent: NATS publishing err=%s\n", err)
			}
		}

		reactor.handleGestureDeviceEvent(ce)

	case "sprite":

		api := "event.sprite"
		x, y, z, err := GetXYZ(api, args)
		if err != nil {
			return nil
		}
		reactor.generateSprite("dummy", x, y, z)

	case "midi":

		// If we're publishing midi events, we ignore ones from ourself
		if r.publishMIDI && nuid == MyNUID() {
			return nil
		}

		switch subEvent {
		case "time_reset":
			log.Printf("HandleSubscribeMIDIInput: montage_time_reset, sending ANO\n")
			reactor.HandleMIDITimeReset()
			reactor.sendANO()

		case "audio_reset":
			log.Printf("HandleSubscribeMIDIInput: montage_audio_reset!!\n")
			r.audioReset()

		default:
			bytes, err := NeedStringArg("bytes", "HandleMIDIEvent", args)
			if err != nil {
				return err
			}
			me, err := r.makeMIDIEvent(subEvent, bytes, args)
			if err != nil {
				return err
			}
			reactor.HandleMIDIDeviceInput(*me)
		}
	}

	return nil
}

func (r *Router) handleGestureDeviceInput(e GestureDeviceEvent) {

	r.eventMutex.Lock()
	defer r.eventMutex.Unlock()

	for _, cb := range r.cursorCallbacks {
		cb(e)
	}

	if r.publishGesture {
		err := PublishGestureDeviceEvent(e)
		if err != nil {
			log.Printf("Router.routeGestureDeviceEvent: NATS publishing err=%s\n", err)
		}
	}

	reactor, ok := r.reactors[e.Region]
	if !ok {
		log.Printf("routeGestureDeviceEvent: no region named %s, unable to process ce=%+v\n", e.Region, e)
		return
	}
	reactor.handleGestureDeviceEvent(e)
}

// makeMIDIEvent xxx
func (r *Router) makeMIDIEvent(subEvent string, bytes string, args map[string]string) (*portmidi.Event, error) {

	var timestamp int64
	s := OptionalStringArg("time", args, "")
	if s != "" {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("makeMIDIEvent: unable to parse time value (%s)", s)
		}
		timestamp = int64(f * 1000.0)
	}

	// We expect the string to start with 0x
	if len(bytes) < 2 || bytes[0:2] != "0x" {
		return nil, fmt.Errorf("makeMIDIEvent: invalid bytes value - %s", bytes)
	}
	hexstring := bytes[2:]
	src := []byte(hexstring)
	bytearr := make([]byte, hex.DecodedLen(len(src)))
	nbytes, err := hex.Decode(bytearr, src)
	if err != nil {
		return nil, fmt.Errorf("makeMIDIEvent: unable to decode hex bytes = %s", bytes)
	}
	status := 0
	data1 := 0
	data2 := 0
	switch nbytes {
	case 0:
		return nil, fmt.Errorf("makeMIDIEvent: unable to handle midi bytes len=%d", nbytes)
	case 1:
		return nil, fmt.Errorf("makeMIDIEvent: unable to handle midi bytes len=%d", nbytes)
	case 2:
		return nil, fmt.Errorf("makeMIDIEvent: unable to handle midi bytes len=%d", nbytes)
	case 3:
		status = int(bytearr[0])
		data1 = int(bytearr[1])
		data2 = int(bytearr[2])
	default:
		return nil, fmt.Errorf("makeMIDIEvent: unable to handle midi bytes len=%d", nbytes)
	}

	me := &portmidi.Event{
		Timestamp: portmidi.Timestamp(timestamp),
		Status:    int64(status),
		Data1:     int64(data1),
		Data2:     int64(data2),
	}
	return me, nil
}

// HandleAPIInput xxx
func (r *Router) HandleAPIInput(executor APIExecutorFunc, data string) (response string) {

	r.eventMutex.Lock()
	defer r.eventMutex.Unlock()

	smap, err := StringMap(data)

	defer func() {
		if DebugUtil.API {
			log.Printf("Router.HandleAPI: response=%s\n", response)
		}
	}()

	if err != nil {
		response = ErrorResponse(err)
		return
	}
	api, ok := smap["api"]
	if !ok {
		response = ErrorResponse(fmt.Errorf("Missing api parameter"))
		return
	}
	nuid, ok := smap["nuid"]
	if !ok {
		response = ErrorResponse(fmt.Errorf("Missing nuid parameter"))
		return
	}
	rawargs, ok := smap["params"]
	if !ok {
		response = ErrorResponse(fmt.Errorf("Missing params parameter"))
		return
	}
	if DebugUtil.API {
		log.Printf("Router.HandleAPI: api=%s args=%s\n", api, rawargs)
	}
	result, err := executor(api, nuid, rawargs)
	if err != nil {
		response = ErrorResponse(err)
	} else {
		response = ResultResponse(result)
	}
	return
}

// HandleOSCInput xxx
func (r *Router) HandleOSCInput(e OSCEvent) {

	r.eventMutex.Lock()
	defer r.eventMutex.Unlock()

	if DebugUtil.OSC {
		log.Printf("Router.HandleOSCInput: msg=%s\n", e.Msg.String())
	}
	switch e.Msg.Address {

	case "/api":
		log.Printf("OSC /api is not implemented\n")
		// r.handleOSCAPI(e.Msg)

	case "/event":
		r.handleOSCEvent(e.Msg)

	default:
		log.Printf("Router.HandleOSCInput: Unrecognized OSC message source=%s msg=%s\n", e.Source, e.Msg)
	}
}

// AddGestureDeviceCallback xxx
func (r *Router) AddGestureDeviceCallback(f GestureDeviceCallbackFunc) {
	r.cursorCallbacks = append(r.cursorCallbacks, f)
}

// ExecuteAPI xxx
func (r *Router) ExecuteAPI(api string, nuid string, rawargs string) (result interface{}, err error) {

	args, err := StringMap(rawargs)
	if err != nil {
		response := ErrorResponse(fmt.Errorf("Router.ExecuteAPI: Unable to interpret value - %s", rawargs))
		log.Printf("Router.ExecuteAPI: bad rawargs value = %s\n", rawargs)
		return response, nil
	}

	result = "0" // most APIs just return 0, so pre-populate it

	words := strings.SplitN(api, ".", 2)
	if len(words) != 2 {
		return nil, fmt.Errorf("Router.ExecuteAPI: api=%s is badly formatted, needs a dot", api)
	}
	apiprefix := words[0]
	apisuffix := words[1]

	if apiprefix == "region" {

		region := OptionalStringArg("region", args, "")
		if region == "" {
			region = r.getRegionForNUID(nuid)
		} else {
			// Remove it from the args given to ExecuteAPI
			delete(args, "region")
		}
		reactor, ok := r.reactors[region]
		if !ok {
			return nil, fmt.Errorf("api/event=%s there is no region named %s", api, region)
		}
		return reactor.ExecuteAPI(apisuffix, args, rawargs)
	}

	// Everything else should be "global", eventually I'll factor this
	if apiprefix != "global" {
		return nil, fmt.Errorf("ExecuteAPI: api=%s unknown apiprefix=%s", api, apiprefix)
	}

	switch apisuffix {

	case "midi_midifile":
		filename, err := NeedStringArg("file", api, args)
		if err != nil {
			break
		}
		midipath := filepath.Join(MIDIFilePath(filename))
		mf, err := NewMIDIFile(midipath)
		if err != nil {
			break
		}
		p := mf.Phrase()
		// Cut it up into 4 channels for the 4 Reactors

		p1 := p.CutSound("channel1")
		if p1.NumNotes() > 0 {
			r.reactors["A"].StartPhrase(p1, "midiplaych1")
		}
		p2 := p.CutSound("channel2")
		if p2.NumNotes() > 0 {
			r.reactors["B"].StartPhrase(p2, "midiplaych2")
		}
		p3 := p.CutSound("channel3")
		if p3.NumNotes() > 0 {
			r.reactors["C"].StartPhrase(p3, "midiplaych3")
		}
		p4 := p.CutSound("channel4")
		if p4.NumNotes() > 0 {
			r.reactors["D"].StartPhrase(p4, "midiplaych4")
		}

	case "echo":
		value, ok := args["value"]
		if !ok {
			value = "ECHO!"
		}
		result = value

	case "debug":
		s, err := NeedStringArg("debug", api, args)
		if err == nil {
			b, err := NeedBoolArg("onoff", api, args)
			if err == nil {
				setDebug(s, b)
			}
		}

	case "set_tempo_factor":
		v, err := NeedFloatArg("value", api, args)
		if err == nil {
			ChangeClicksPerSecond(float64(v))
		}

	case "audio_reset":
		r.audioReset()

	case "recordingStart":
		r.recordingOn = true
		if r.recordingFile != nil {
			log.Printf("Hey, recordingFile wasn't nil?\n")
			r.recordingFile.Close()
		}
		r.recordingFile, err = os.Create(recordingsFile("LastRecording.json"))
		if err != nil {
			return nil, err
		}
		r.recordingBegun = time.Now()
		if r.recordingOn {
			r.recordEvent("global", "*", "start", "{}")
		}
	case "recordingSave":
		name, err := NeedStringArg("name", api, args)
		if err == nil {
			err = r.recordingSave(name)
		}

	case "recordingStop":
		if r.recordingOn {
			r.recordEvent("global", "*", "stop", "{}")
		}
		if r.recordingFile != nil {
			r.recordingFile.Close()
			r.recordingFile = nil
		}
		r.recordingOn = false

	case "recordingPlay":
		name, err := NeedStringArg("name", api, args)
		if err == nil {
			events, err := r.recordingLoad(name)
			if err == nil {
				r.sendANO()
				go r.recordingPlayback(events)
			}
		}

	case "recordingPlaybackStop":
		r.recordingPlaybackStop()

	default:
		log.Printf("Router.ExecuteAPI api=%s is not recognized\n", api)
		err = fmt.Errorf("Router.ExecuteAPI unrecognized api=%s", api)
		result = ""
	}

	return result, err
}

func (r *Router) advanceClickTo(toClick Clicks) {

	// Don't let events get handled while we're advancing
	r.eventMutex.Lock()
	defer r.eventMutex.Unlock()

	for clk := r.lastClick; clk < toClick; clk++ {
		for _, reactor := range r.reactors {
			if (clk % oneBeat) == 0 {
				reactor.checkGestureUp()
			}
			reactor.AdvanceByOneClick()
		}
	}
	r.lastClick = toClick
}

func (r *Router) recordEvent(eventType string, pad string, method string, args string) {
	if r.recordingOn == false {
		log.Printf("HEY! recordEvent called when recordingOn is false!?\n")
		return
	}
	if r.recordingFile == nil {
		log.Printf("HEY! recordEvent called when recordingFile is nil!?\n")
		return
	}
	if args[0] != '{' {
		log.Printf("HEY! first char of args in recordEvent needs to be a curly brace!\n")
		return
	}
	if r.recordingFile != nil {
		dt := time.Since(r.recordingBegun).Seconds()
		r.recordingFile.WriteString(
			fmt.Sprintf("%.6f %s %s %s ", dt, eventType, pad, method) + args + "\n")
		r.recordingFile.Sync()
	}
}

func (r *Router) recordPadAPI(data string) {
	if r.recordingOn {
		r.recordEvent("pad", "*", "api", data)
	}
}

func (r *Router) recordingSave(name string) error {
	if r.recordingFile != nil {
		r.recordingFile.Close()
		r.recordingFile = nil
	}

	source, err := os.Open(recordingsFile("LastRecording.json"))
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(recordingsFile(fmt.Sprintf("%s.json", name)))
	if err != nil {
		return err
	}
	defer destination.Close()
	nBytes, err := io.Copy(destination, source)
	if err != nil {
		return err
	}
	log.Printf("nbytes copied = %d\n", nBytes)
	return nil
}

func (r *Router) recordingPlaybackStop() {
	r.killPlayback = true
	r.sendANO()
}

func (r *Router) sendANO() {
	for _, reactor := range r.reactors {
		reactor.sendANO()
	}
}

func (r *Router) notifyGUI(eventName string) {
	if !ConfigBool("notifygui") {
		return
	}
	msg := osc.NewMessage("/notify")
	msg.Append(eventName)
	r.guiClient.Send(msg)
	if DebugUtil.OSC {
		log.Printf("Router.notifyGUI: msg=%v\n", msg)
	}
}

func (r *Router) audioReset() {
	msg := osc.NewMessage("/play")
	msg.Append(int32(0))
	r.plogueClient.Send(msg)
	// Give Plogue time to react
	time.Sleep(400 * time.Millisecond)
	msg = osc.NewMessage("/play")
	msg.Append(int32(1))
	r.plogueClient.Send(msg)
}

func (r *Router) recordingPlayback(events []*PlaybackEvent) error {

	// XXX - WARNING, this code hasn't been exercised in a LONG time,
	// XXX - someday it'll probably be resurrected.
	log.Printf("recordingPlay, #events = %d\n", len(events))
	r.killPlayback = false

	r.notifyGUI("start")

	playbackBegun := time.Now()

	for _, pe := range events {
		if r.killPlayback {
			log.Printf("killPlayback!\n")
			r.sendANO()
			break
		}
		if pe != nil {
			eventTime := (*pe).time // in seconds
			for time.Since(playbackBegun).Seconds() < eventTime {
				time.Sleep(time.Millisecond)
			}
			eventType := (*pe).eventType
			pad := (*pe).pad
			var reactor *Reactor
			if pad != "*" {
				reactor = r.reactors[pad]
			}
			method := (*pe).method
			// time := (*pe).time
			args := (*pe).args
			rawargs := (*pe).rawargs
			// log.Printf("eventType=%s method=%s\n", eventType, method)
			switch eventType {
			case "cursor":
				id := args["id"]
				x := args["x"]
				y := args["y"]
				z := args["z"]
				ddu := method
				xf, _ := ParseFloat32(x, "cursor.x")
				yf, _ := ParseFloat32(y, "cursor.y")
				zf, _ := ParseFloat32(z, "cursor.z")
				reactor.executeIncomingGesture(GestureStepEvent{
					ID:         id,
					X:          xf,
					Y:          yf,
					Z:          zf,
					Downdragup: ddu,
				})
			case "api":
				// since we already have args
				reactor.ExecuteAPI(method, args, rawargs)
			case "global":
				log.Printf("NOT doing anying for global playback, method=%s\n", method)
			default:
				log.Printf("Unknown eventType=%s in recordingPlay\n", eventType)
			}
		}
	}
	log.Printf("recordingPlay has finished!\n")
	r.notifyGUI("stop")
	return nil
}

func (r *Router) recordingLoad(name string) ([]*PlaybackEvent, error) {
	file, err := os.Open(recordingsFile(fmt.Sprintf("%s.json", name)))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	events := make([]*PlaybackEvent, 0)
	nlines := 0
	var line string
	for {
		line, err = reader.ReadString('\n')
		if err != nil {
			break
		}
		nlines++
		words := strings.SplitN(line, " ", 5)
		if len(words) < 4 {
			log.Printf("recordings line has less than 3 words? line=%s\n", line)
			continue
		}
		evTime, err := strconv.ParseFloat(words[0], 64)
		if err != nil {
			log.Printf("Unable to convert time in playback file: %s\n", words[0])
			continue
		}
		playbackType := words[1]
		switch playbackType {
		case "cursor", "sound", "visual", "effect", "global", "perform":
			pad := words[2]
			method := words[3]
			rawargs := words[4]
			args, err := StringMap(rawargs)
			if err != nil {
				log.Printf("Unable to parse JSON: %s\n", rawargs)
				continue
			}
			pe := &PlaybackEvent{time: evTime, eventType: playbackType, pad: pad, method: method, args: args, rawargs: rawargs}
			events = append(events, pe)
		default:
			log.Printf("Unknown playbackType in playback file: %s\n", playbackType)
			continue
		}
	}
	if err != io.EOF {
		return nil, err
	}
	log.Printf("Number of playback events is %d, lines is %d\n", len(events), nlines)
	return events, nil
}

func (r *Router) handleOSCEvent(msg *osc.Message) {

	tags, _ := msg.TypeTags()
	_ = tags
	nargs := msg.CountArguments()
	if nargs < 1 {
		log.Printf("Router.handleOSCGestureEvent: too few arguments\n")
		return
	}
	rawargs, err := ArgAsString(msg, 0)
	if err != nil {
		log.Printf("Router.handleOSCGestureEvent: err=%s\n", err)
		return
	}
	if len(rawargs) == 0 || rawargs[0] != '{' {
		log.Printf("Router.handleOSCGestureEvent: first char of args must be curly brace\n")
		return
	}

	// Add the required nuid argument, which OSC input doesn't provide
	newrawargs := "{ \"nuid\": \"" + MyNUID() + "\", " + rawargs[1:]
	args, err := StringMap(newrawargs)

	err = r.HandleSubscribedEventArgs(args)
	if err != nil {
		log.Printf("Router.handleOSCGestureEvent: err=%s\n", err)
		return
	}
}

func (r *Router) handleOSCSpriteEvent(msg *osc.Message) {

	tags, _ := msg.TypeTags()
	_ = tags
	nargs := msg.CountArguments()
	if nargs < 1 {
		log.Printf("Router.handleOSCSpriteEvent: too few arguments\n")
		return
	}
	rawargs, err := ArgAsString(msg, 0)
	if err != nil {
		log.Printf("Router.handleOSCSpriteEvent: err=%s\n", err)
		return
	}
	if len(rawargs) == 0 || rawargs[0] != '{' {
		log.Printf("Router.handleOSCSpriteEvent: first char of args must be curly brace\n")
		return
	}

	// Add the required nuid argument, which OSC input doesn't provide
	newrawargs := "{ \"nuid\": \"" + MyNUID() + "\", " + rawargs[1:]
	args, err := StringMap(newrawargs)
	if err != nil {
		log.Printf("Router.handleOSCSpriteEvent: Unable to process args=%s\n", newrawargs)
		return
	}

	err = r.HandleSubscribedEventArgs(args)
	if err != nil {
		log.Printf("Router.handleOSCSpriteEvent: err=%s\n", err)
		return
	}
}

// No error return because it's OSC
func (r *Router) handleOSCAPI(msg *osc.Message) {
	tags, _ := msg.TypeTags()
	_ = tags
	nargs := msg.CountArguments()
	if nargs < 1 {
		log.Printf("Router.handleOSCAPI: too few arguments\n")
		return
	}
	api, err := ArgAsString(msg, 0)
	if err != nil {
		log.Printf("Router.handleOSCAPI: err=%s\n", err)
		return
	}
	rawargs, err := ArgAsString(msg, 1)
	if err != nil {
		log.Printf("Router.handleOSCAPI: err=%s\n", err)
		return
	}
	if len(rawargs) == 0 || rawargs[0] != '{' {
		log.Printf("Router.handleOSCAPI: first char of args must be curly brace\n")
		return
	}
	_, err = r.ExecuteAPI(api, MyNUID(), rawargs)
	if err != nil {
		log.Printf("Router.handleOSCAPI: err=%s", err)
	}
	return
}

// OLDgetReactorForNUID - NOTE, this removes the "nuid" argument from the map,
// partially to avoid (at least until there's a reason for) letting Reactor APIs
// know what source is calling them.  I.e. all source-depdendent behaviour is
// determined in Router.
func (r *Router) OLDgetReactorForNUID(api string, args map[string]string) (*Reactor, error) {
	nuid, ok := args["nuid"]
	if !ok {
		return nil, fmt.Errorf("api/event=%s missing value for nuid", api)
	}
	delete(args, "nuid") // see comment above
	region := r.getRegionForNUID(nuid)
	reactor, ok := TheRouter().reactors[region]
	if !ok {
		return nil, fmt.Errorf("api/event=%s there is no region named %s", api, region)
	}
	return reactor, nil
}

// availableRegion - return the name of a region that hasn't been assigned to a remote yet
// It is assumed that we have a mutex on r.regionAssignedToSource
func (r *Router) availableRegion(source string) string {

	nregions := len(r.regionLetters)
	alreadyAssigned := make([]bool, nregions)
	for _, v := range r.regionAssignedToNUID {
		i := strings.Index(r.regionLetters, v)
		if i >= 0 {
			alreadyAssigned[i] = true
		}
	}
	for i, used := range alreadyAssigned {
		if !used {
			avail := r.regionLetters[i : i+1]
			if DebugUtil.Gesture {
				log.Printf("Router.assignRegion: %s is assigned to source=%s\n", avail, source)
			}
			return avail
		}
	}
	if DebugUtil.Gesture {
		log.Printf("Router.assignRegion: No regions available\n")
	}
	return ""
}

/*
func (r *Router) getRegionForSource(fullsource string) string {

	r.regionAssignedMutex.Lock()
	defer r.regionAssignedMutex.Unlock()

	// A couple different types of source strings.
	// Anyting starting with SM is a Sensel Morph serial#
	if fullsource[:2] == "SM" {
		return r.getRegionForMorph(fullsource)
	}
	return r.getRegionForNUID(fullsource)
}
*/

// This is used only at startup to pre-seed the
// Regions for known Morphs (e.g for Space Montage Pro).
func (r *Router) setRegionForMorph(serialnum string, region string) {

	r.regionAssignedMutex.Lock()
	defer r.regionAssignedMutex.Unlock()

	log.Printf("setRegionForMorph: Assigning region=%s to serialnum=%s\n", region, serialnum)
	r.regionForMorph[serialnum] = region
}

func (r *Router) assignRegionForMorph(serialnum string, region string) {
	r.regionForMorph[serialnum] = region
}

// Assumes we have the r.regionAssignedMutex
func (r *Router) getRegionForNUID(nuid string) string {

	// See if we've already assigned a region to this source
	region, ok := r.regionAssignedToNUID[nuid]
	if ok {
		return region
	}

	// Hasn't been seen yet, let's get an available region
	region = r.availableRegion(nuid)
	log.Printf("getRegionForNUID: Assigning region=%s to nuid=%s\n", region, nuid)
	r.regionAssignedToNUID[nuid] = region

	return region
}

func cursorid(sid int, source string) string {
	return fmt.Sprintf("%d@%s", sid, source)
}
