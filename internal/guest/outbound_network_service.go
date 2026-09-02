package guest

import (
	"fmt"
	"os/exec"
)

type HostOutboundNetworkService struct{}

func (HostOutboundNetworkService) PrepareOutboundNetwork(outboundNetwork OutboundNetwork) error {
	if !outboundNetwork.Enabled {
		return nil
	}
	_ = runHostNetworkCommand("ip", "link", "delete", outboundNetwork.HostDeviceName)
	if errorValue := runHostNetworkCommand("ip", "tuntap", "add", "dev", outboundNetwork.HostDeviceName, "mode", "tap"); errorValue != nil {
		return errorValue
	}
	if errorValue := runHostNetworkCommand("ip", "addr", "replace", outboundNetwork.HostAddressCIDR, "dev", outboundNetwork.HostDeviceName); errorValue != nil {
		_ = runHostNetworkCommand("ip", "link", "delete", outboundNetwork.HostDeviceName)
		return errorValue
	}
	if errorValue := runHostNetworkCommand("ip", "link", "set", outboundNetwork.HostDeviceName, "up"); errorValue != nil {
		_ = runHostNetworkCommand("ip", "link", "delete", outboundNetwork.HostDeviceName)
		return errorValue
	}
	if errorValue := runHostNetworkCommand("sysctl", "-w", "net.ipv4.ip_forward=1"); errorValue != nil {
		_ = runHostNetworkCommand("ip", "link", "delete", outboundNetwork.HostDeviceName)
		return errorValue
	}
	if errorValue := ensureHostNetworkRule("iptables", "-t", "nat", "-C", "POSTROUTING", "-s", outboundNetwork.NetworkCIDR, "-j", "MASQUERADE"); errorValue != nil {
		_ = runHostNetworkCommand("ip", "link", "delete", outboundNetwork.HostDeviceName)
		return errorValue
	}
	if errorValue := ensureHostNetworkRule("iptables", "-C", "FORWARD", "-i", outboundNetwork.HostDeviceName, "-j", "ACCEPT"); errorValue != nil {
		_ = runHostNetworkCommand("ip", "link", "delete", outboundNetwork.HostDeviceName)
		return errorValue
	}
	if errorValue := ensureHostNetworkRule("iptables", "-C", "FORWARD", "-o", outboundNetwork.HostDeviceName, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"); errorValue != nil {
		_ = runHostNetworkCommand("ip", "link", "delete", outboundNetwork.HostDeviceName)
		return errorValue
	}
	if errorValue := ensureHostNetworkRule("iptables", "-t", "mangle", "-C", "FORWARD", "-p", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-j", "TCPMSS", "--clamp-mss-to-pmtu"); errorValue != nil {
		fmt.Println("forwarded TCP MSS clamp unavailable; continuing without it:", errorValue)
	}
	return nil
}

func (HostOutboundNetworkService) CleanupOutboundNetwork(outboundNetwork OutboundNetwork) error {
	if !outboundNetwork.Enabled {
		return nil
	}
	_ = runHostNetworkCommand("iptables", "-t", "mangle", "-D", "FORWARD", "-p", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-j", "TCPMSS", "--clamp-mss-to-pmtu")
	_ = runHostNetworkCommand("iptables", "-D", "FORWARD", "-o", outboundNetwork.HostDeviceName, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT")
	_ = runHostNetworkCommand("iptables", "-D", "FORWARD", "-i", outboundNetwork.HostDeviceName, "-j", "ACCEPT")
	_ = runHostNetworkCommand("iptables", "-t", "nat", "-D", "POSTROUTING", "-s", outboundNetwork.NetworkCIDR, "-j", "MASQUERADE")
	_ = runHostNetworkCommand("ip", "link", "delete", outboundNetwork.HostDeviceName)
	return nil
}

func ensureHostNetworkRule(name string, checkArguments ...string) error {
	if runHostNetworkCommand(name, checkArguments...) == nil {
		return nil
	}
	addArguments := append([]string{}, checkArguments...)
	for index, argument := range addArguments {
		if argument == "-C" {
			addArguments[index] = "-A"
			break
		}
	}
	return runHostNetworkCommand(name, addArguments...)
}

func runHostNetworkCommand(name string, arguments ...string) error {
	command := exec.Command(name, arguments...)
	output, errorValue := command.CombinedOutput()
	if errorValue != nil {
		return fmt.Errorf("%s %v failed: %w: %s", name, arguments, errorValue, string(output))
	}
	return nil
}
