//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const svcName = "PrzywolaniaSerwer"

func serviceUsage() {
	fmt.Fprintln(os.Stderr, "  przywolania-serwer install [opcje]    zainstaluj jako usluge Windows (start automatyczny)")
	fmt.Fprintln(os.Stderr, "  przywolania-serwer uninstall          usun usluge")
}

type winService struct{}

func (winService) Execute(args []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	s <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runServer(ctx) }()
	s <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				s <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				s <- svc.Status{State: svc.StopPending}
				cancel()
				<-done
				return false, 0
			}
		case err := <-done:
			if err != nil {
				return false, 1
			}
			return false, 0
		}
	}
}

func serviceMain(args []string) (bool, error) {
	if isSvc, err := svc.IsWindowsService(); err == nil && isSvc {
		return true, svc.Run(svcName, winService{})
	}
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "install":
		exe, err := os.Executable()
		if err != nil {
			return true, err
		}
		m, err := mgr.Connect()
		if err != nil {
			return true, fmt.Errorf("uruchom jako administrator: %w", err)
		}
		defer m.Disconnect()
		if s, err := m.OpenService(svcName); err == nil {
			s.Close()
			return true, fmt.Errorf("usluga %s juz istnieje", svcName)
		}
		// przekazujemy opcje (-addr, -data) do uslugi
		var extra []string
		for _, a := range os.Args[1:] {
			if a != "install" {
				extra = append(extra, a)
			}
		}
		s, err := m.CreateService(svcName, exe, mgr.Config{
			DisplayName: "Przywolania do stolika pakujacego",
			Description: "Serwer przywolan: tablety na stanowiskach pakujacych -> terminale pracownikow",
			StartType:   mgr.StartAutomatic,
		}, extra...)
		if err != nil {
			return true, err
		}
		defer s.Close()
		_ = s.SetRecoveryActions([]mgr.RecoveryAction{
			{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
			{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
			{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		}, 86400)
		if err := s.Start(); err != nil {
			return true, err
		}
		fmt.Println("Usluga zainstalowana i uruchomiona:", svcName, strings.Join(extra, " "))
		return true, nil
	case "uninstall":
		m, err := mgr.Connect()
		if err != nil {
			return true, fmt.Errorf("uruchom jako administrator: %w", err)
		}
		defer m.Disconnect()
		s, err := m.OpenService(svcName)
		if err != nil {
			return true, fmt.Errorf("brak uslugi %s", svcName)
		}
		defer s.Close()
		_, _ = s.Control(svc.Stop)
		time.Sleep(2 * time.Second)
		if err := s.Delete(); err != nil {
			return true, err
		}
		fmt.Println("Usluga usunieta")
		return true, nil
	}
	return false, nil
}
