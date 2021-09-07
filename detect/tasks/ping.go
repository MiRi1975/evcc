package tasks

import (
	"runtime"
	"time"

	"github.com/evcc-io/evcc/util"
	"github.com/go-ping/ping"
)

const Ping TaskType = "ping"

func init() {
	registry.Add(Ping, PingHandlerFactory)
}

func PingHandlerFactory(conf map[string]interface{}) (TaskHandler, error) {
	handler := PingHandler{
		Count:   1,
		Timeout: timeout,
	}

	err := util.DecodeOther(conf, &handler)

	return &handler, err
}

type PingHandler struct {
	Count   int
	Timeout time.Duration
}

func (h *PingHandler) Test(log *util.Logger, in ResultDetails) []ResultDetails {
	pinger, err := ping.NewPinger(in.IP)
	if err != nil {
		panic(err)
	}

	if runtime.GOOS == "windows" {
		pinger.SetPrivileged(true)
	}

	pinger.Count = h.Count
	pinger.Timeout = h.Timeout

	if err = pinger.Run(); err != nil {
		log.Errorln("ping:", err)

		if runtime.GOOS != "windows" {
			log.Errorln("")
			log.Errorln("In order to run evcc in discovery mode, make sure to allow ping:")
			log.Errorln("")
			log.Errorln("	sudo sysctl -w net.ipv4.ping_group_range=\"0 2147483647\"")
		}

		log.Fatalln("")
	}

	stat := pinger.Statistics()

	if stat.PacketsRecv == 0 {
		return nil
	}

	return []ResultDetails{in}
}
