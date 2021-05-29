package main

import (
	"context"
	"github.com/huin/goupnp"
	"golang.org/x/sync/errgroup"
	"log"

	"github.com/huin/goupnp/dcps/internetgateway2"
)

type RouterClient interface {
	AddPortMapping(
		NewRemoteHost string,
		NewExternalPort uint16,
		NewProtocol string,
		NewInternalPort uint16,
		NewInternalClient string,
		NewEnabled bool,
		NewPortMappingDescription string,
		NewLeaseDuration uint32,
	) (err error)

	GetExternalIPAddress() (
		NewExternalIPAddress string,
		err error,
	)

	GetServiceClient() *goupnp.ServiceClient
}

func pickRouterClients() ([]RouterClient, error) {
	tasks, _ := errgroup.WithContext(context.Background())
	var clients []RouterClient

	tasks.Go(func() error {
		cs, _, err := internetgateway2.NewWANIPConnection1Clients()
		for _, c := range cs {
			clients = append(clients, c)
		}
		return err
	})

	tasks.Go(func() error {
		cs, _, err := internetgateway2.NewWANIPConnection2Clients()
		for _, c := range cs {
			clients = append(clients, c)
		}
		return err
	})

	tasks.Go(func() error {
		cs, _, err := internetgateway2.NewWANPPPConnection1Clients()
		for _, c := range cs {
			clients = append(clients, c)
		}
		return err
	})

	if err := tasks.Wait(); err != nil {
		return nil, err
	}

	return clients, nil
}

func addUDPPortMapping(localIP string, port uint16) {
	clients, err := pickRouterClients()
	if err != nil {
		log.Println(err)
		return
	}
	log.Printf("%v Router(s) found.", len(clients))

	for _, c := range clients {
		err = c.AddPortMapping( "", port, "UDP", port, localIP, true, "zdxsv udp proxy port mapping.", 3600*24)
		if err != nil {
			log.Println("addUDPPortMapping error: ", err)
		} else {
			model := ""
			if c.GetServiceClient() != nil && c.GetServiceClient().RootDevice != nil {
				model = c.GetServiceClient().RootDevice.Device.ModelName
			}
			log.Println("UPnPによりポート開放しました", model, localIP, port)
		}
	}

}