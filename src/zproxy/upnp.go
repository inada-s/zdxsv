package main

import (
	"log"

	"github.com/huin/goupnp/dcps/internetgateway2"
)

func addUDPPortMapping(localIP string, port uint16) {
	clients, errors, err := internetgateway2.NewWANPPPConnection1Clients()
	if err != nil {
		log.Println(err)
		return
	}

	log.Printf("Got %d errors finding servers and %d successfully discovered.\n",
		len(errors), len(clients))

	for i, e := range errors {
		log.Printf("Error finding server #%d: %v\n", i+1, e)
	}

	for _, c := range clients {
		dev := &c.ServiceClient.RootDevice.Device
		srv := c.ServiceClient.Service
		scpd, err := srv.RequestSCDP()
		if err != nil {
			log.Println(err)
			continue
		}
		log.Println(dev.FriendlyName, " :: ", srv.String())
		if scpd == nil || scpd.GetAction("AddPortMapping") != nil {
			err := c.AddPortMapping("", port, "UDP", port, localIP, true, "zdxsv udp proxy port mapping.", 3600 * 24)
			if err != nil {
				log.Println("addUDPPortMapping error: ", err)
			} else {
				log.Println("UPnPによりポート開放しました", dev.ModelName, localIP, port)
			}
		}
	}
}

func delUDPPortMapping(localIP string, port uint16) {
	clients, errors, err := internetgateway2.NewWANPPPConnection1Clients()
	if err != nil {
		log.Println(err)
		return
	}

	log.Printf("Got %d errors finding servers and %d successfully discovered.\n",
		len(errors), len(clients))

	for i, e := range errors {
		log.Printf("Error finding server #%d: %v\n", i+1, e)
	}

	for _, c := range clients {
		dev := &c.ServiceClient.RootDevice.Device
		srv := c.ServiceClient.Service
		scpd, err := srv.RequestSCDP()
		if err != nil {
			log.Println(err)
			continue
		}
		log.Println(dev.FriendlyName, " :: ", srv.String())
		if scpd == nil || scpd.GetAction("DeletePortMapping") != nil {
			err := c.DeletePortMapping("", port, "UDP")
			log.Println("delUDPPortMapping: ", err)
		}
	}
}
