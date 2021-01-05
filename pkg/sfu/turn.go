package sfu

import (
	"net"
	"regexp"
	"strings"

	log "github.com/pion/ion-log"
	"github.com/pion/turn/v2"
)

// WebRTCConfig defines parameters for ice
type TurnConfig struct {
	Enabled     bool     `mapstructure:"enabled"`
	Realm       string   `mapstructure:"realm"`
	Address     string   `mapstructure:"address"`
	Credentials string   `mapstructure:"credentials"`
	PortRange   []uint16 `mapstructure:"portrange"`
}

func initTurnServer(conf TurnConfig, auth func(username, real string, srcAddr net.Addr) ([]byte, bool)) (*turn.Server, error) {
	// Create a UDP listener to pass into pion/turn
	// pion/turn itself doesn't allocate any UDP sockets, but lets the user pass them in
	// this allows us to add logging, storage or modify inbound/outbound traffic
	udpListener, err := net.ListenPacket("udp4", conf.Address)
	if err != nil {
		return nil, err
	}

	if auth == nil {
		usersMap := map[string][]byte{}
		for _, kv := range regexp.MustCompile(`(\w+)=(\w+)`).FindAllStringSubmatch(conf.Credentials, -1) {
			usersMap[kv[1]] = turn.GenerateAuthKey(kv[1], conf.Realm, kv[2])
		}
		if len(usersMap) == 0 {
			log.Panicf("No turn auth provided")
		}
		auth = func(username string, realm string, srcAddr net.Addr) ([]byte, bool) {
			if key, ok := usersMap[username]; ok {
				return key, true
			}
			return nil, false
		}
	}

	var minPort, maxPort uint16
	if conf.Enabled && len(conf.PortRange) == 2 {
		minPort = conf.PortRange[0]
		maxPort = conf.PortRange[1]
	}

	return turn.NewServer(turn.ServerConfig{
		Realm: conf.Realm,
		// Set AuthHandler callback
		// This is called everytime a user tries to authenticate with the TURN server
		// Return the key for that user, or false when no user is found
		AuthHandler: auth,
		// PacketConnConfigs is a list of UDP Listeners and the configuration around them
		PacketConnConfigs: []turn.PacketConnConfig{
			{
				PacketConn: udpListener,
				RelayAddressGenerator: &turn.RelayAddressGeneratorPortRange{
					RelayAddress: net.ParseIP(strings.Split(conf.Address, ":")[0]), // Claim that we are listening on IP passed by user (This should be your Public IP)
					Address:      "0.0.0.0",                                        // But actually be listening on every interface
					MinPort:      minPort,
					MaxPort:      maxPort,
				},
			},
		},
	})
}
