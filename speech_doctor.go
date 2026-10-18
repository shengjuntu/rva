package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os/exec"
	"time"
)

type speechDoctorCheck struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
}
type speechDoctorReport struct {
	Version         string              `json:"version"`
	Backend         string              `json:"backend"`
	Model           string              `json:"model,omitempty"`
	Voice           string              `json:"voice,omitempty"`
	Ready           bool                `json:"ready"`
	SynthesisTested bool                `json:"synthesisTested"`
	Checks          []speechDoctorCheck `json:"checks"`
	Voices          []string            `json:"voices,omitempty"`
}

// A read-only diagnostic: no app startup, data migration, GPU synthesis or keys
// in its output. Run it in the same environment/container as Video App.
func speechDoctor(ctx context.Context, c SpeechConfig) speechDoctorReport {
	r := speechDoctorReport{Version: version, Backend: c.Backend, Ready: true}
	add := func(name, state, message string) {
		r.Checks = append(r.Checks, speechDoctorCheck{name, state, message})
		if state == "error" {
			r.Ready = false
		}
	}
	if e := validateSpeechConfig(c); e != nil {
		add("configuration", "error", e.Error())
	} else {
		add("configuration", "ready", "")
	}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if _, e := exec.LookPath(name); e != nil {
			add(name, "error", "未在 PATH 找到可执行文件")
		} else {
			add(name, "ready", "")
		}
	}
	if c.Backend == "espeak-ng" {
		if _, e := exec.LookPath("espeak-ng"); e != nil {
			add("espeak-ng", "error", "未在 PATH 找到可执行文件")
		} else {
			add("espeak-ng", "ready", "")
		}
		return r
	}
	r.Model, r.Voice = c.Model, c.Voice
	if e := validateSpeechSettings(c, false); e != nil {
		return r
	}
	state, e := probeIndexTTS(ctx, c)
	if e != nil {
		if state != "unknown" {
			state = "error"
		}
		add("models", state, e.Error())
		r.Ready = false // An unknown model endpoint is not proof of connectivity.
	} else {
		add("models", "ready", "")
	}
	catalog, e := listSpeechVoices(ctx, c)
	if e != nil {
		add("voices", "error", e.Error())
		return r
	}
	r.Voices = catalog.Voices
	if !catalog.Supported {
		add("voices", "unknown", catalog.Message)
	} else {
		found := false
		for _, voice := range catalog.Voices {
			if voice == c.Voice {
				found = true
			}
		}
		if found {
			add("voices", "ready", "")
		} else {
			add("voices", "error", "默认音色未在服务音色列表中，请核对配置")
		}
	}
	return r
}

func runSpeechDoctor(args []string, output, stderr io.Writer) int {
	flags := flag.NewFlagSet("speech-doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	data := flags.String("data", "./data", "Video App data directory (read only); VIDEO_TTS_* environment overrides apply")
	if e := flags.Parse(args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		return 2
	}
	a := &App{data: *data}
	configErr := a.loadSpeechConfig()
	if configErr == nil {
		configErr = a.speechEnvironment()
	}
	var report speechDoctorReport
	if configErr != nil {
		report = speechDoctorReport{Version: version, Checks: []speechDoctorCheck{{"configuration", "error", configErr.Error()}}}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		report = speechDoctor(ctx, a.tts)
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if encoder.Encode(report) != nil {
		return 1
	}
	if !report.Ready {
		return 1
	}
	return 0
}
