package gost

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AliceNetworks/gost-panel/internal/model"
)

// ConfigGenerator GOST 配置生成器
type ConfigGenerator struct{}

func NewConfigGenerator() *ConfigGenerator {
	return &ConfigGenerator{}
}

// GenerateNodeConfig 生成节点完整配置
func (g *ConfigGenerator) GenerateNodeConfig(node *model.Node) map[string]interface{} {
	return g.GenerateNodeConfigWithRules(node, nil, nil, nil)
}

// GenerateNodeConfigWithRules 生成节点完整配置 (含分流/准入/主机映射/反向代理/记录器规则)
func (g *ConfigGenerator) GenerateNodeConfigWithRules(node *model.Node, bypasses []model.Bypass, admissions []model.Admission, hostMappings []model.HostMapping, ingresses ...[]model.Ingress) map[string]interface{} {
	config := map[string]interface{}{}

	// API 配置
	config["api"] = g.generateAPIConfig(node)

	// Metrics 配置
	config["metrics"] = map[string]interface{}{
		"addr": ":9000",
	}

	// Observer 配置
	config["observers"] = g.generateObservers(node)

	// 服务配置 (SOCKS5 代理服务，bind=true 支持反向隧道)
	mainService := g.generateMainService(node)

	// 添加 bypass 引用到 handler
	if len(bypasses) > 0 {
		bypassName := fmt.Sprintf("bypass-%d", node.ID)
		if handler, ok := mainService["handler"].(map[string]interface{}); ok {
			handler["bypass"] = bypassName
		}
	}

	// 添加 admission 引用到 service
	if len(admissions) > 0 {
		admissionName := fmt.Sprintf("admission-%d", node.ID)
		mainService["admission"] = admissionName
	}

	// 添加 hosts 引用到 handler
	if len(hostMappings) > 0 {
		hostsName := fmt.Sprintf("hosts-%d", node.ID)
		if handler, ok := mainService["handler"].(map[string]interface{}); ok {
			handler["hosts"] = hostsName
		}
	}

	services := []map[string]interface{}{mainService}
	config["services"] = services

	// 认证器配置
	if node.ProxyUser != "" {
		config["authers"] = g.generateAuthers(node)
	}

	// 限速配置
	if node.SpeedLimit > 0 || node.ConnRateLimit > 0 {
		config["limiters"] = g.generateLimiters(node)
		config["rlimiters"] = g.generateRateLimiters(node)
	}

	// DNS 配置
	if node.DNSServer != "" {
		config["resolvers"] = g.generateResolvers(node)
	}

	// Bypass 配置
	if len(bypasses) > 0 {
		config["bypasses"] = g.generateBypassConfigs(node.ID, bypasses)
	}

	// Admission 配置
	if len(admissions) > 0 {
		config["admissions"] = g.generateAdmissionConfigs(node.ID, admissions)
	}

	// Hosts 配置
	if len(hostMappings) > 0 {
		config["hosts"] = g.generateHostsConfigs(node.ID, hostMappings)
	}

	// Ingress 配置
	if len(ingresses) > 0 && len(ingresses[0]) > 0 {
		config["ingresses"] = g.generateIngressConfigs(node.ID, ingresses[0])
	}

	return config
}

// generateAPIConfig 生成 API 配置
func (g *ConfigGenerator) generateAPIConfig(node *model.Node) map[string]interface{} {
	api := map[string]interface{}{
		"addr": fmt.Sprintf(":%d", node.APIPort),
	}
	if node.APIUser != "" {
		api["auth"] = map[string]string{
			"username": node.APIUser,
			"password": node.APIPass,
		}
	}
	return api
}

// generateObservers 生成 Observer 配置
func (g *ConfigGenerator) generateObservers(node *model.Node) []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name": "stats-observer",
			"plugin": map[string]interface{}{
				"type": "http",
				"addr": fmt.Sprintf("http://127.0.0.1:%d/observers/stats-observer", node.APIPort),
			},
		},
	}
}

// generateMainService 生成主服务配置
func (g *ConfigGenerator) generateMainService(node *model.Node) map[string]interface{} {
	service := map[string]interface{}{
		"name":     "main-service",
		"addr":     fmt.Sprintf(":%d", node.Port),
		"observer": "stats-observer",
	}

	// Handler 配置
	service["handler"] = g.generateHandler(node)

	// Listener 配置
	service["listener"] = g.generateListener(node)

	// 限速器
	if node.SpeedLimit > 0 {
		service["limiter"] = "speed-limiter"
	}
	if node.ConnRateLimit > 0 {
		service["rlimiter"] = "rate-limiter"
	}

	// PROXY Protocol
	if node.ProxyProtocol > 0 {
		if listener, ok := service["listener"].(map[string]interface{}); ok {
			metadata, _ := listener["metadata"].(map[string]interface{})
			if metadata == nil {
				metadata = map[string]interface{}{}
			}
			metadata["proxyProtocol"] = true
			listener["metadata"] = metadata
		}
	}

	// 探测抵抗
	if node.ProbeResist != "" {
		if handler, ok := service["handler"].(map[string]interface{}); ok {
			metadata, _ := handler["metadata"].(map[string]interface{})
			if metadata == nil {
				metadata = map[string]interface{}{}
			}
			probeResist := node.ProbeResist
			if node.ProbeResistValue != "" {
				probeResist += ":" + node.ProbeResistValue
			}
			metadata["probeResist"] = probeResist
			handler["metadata"] = metadata
		}
	}

	return service
}

// generateRelayService 生成 relay 服务用于反向隧道
func (g *ConfigGenerator) generateRelayService(node *model.Node) map[string]interface{} {
	// relay 服务端口 = 主端口 + 1000
	relayPort := node.Port + 1000

	service := map[string]interface{}{
		"name": "relay-service",
		"addr": fmt.Sprintf(":%d", relayPort),
		"handler": map[string]interface{}{
			"type": "relay",
		},
		"listener": map[string]interface{}{
			"type": "tcp",
		},
	}

	return service
}

// generateHandler 生成 Handler 配置
func (g *ConfigGenerator) generateHandler(node *model.Node) map[string]interface{} {
	handler := map[string]interface{}{}

	switch node.Protocol {
	case "http":
		handler["type"] = "http"
		if node.ProxyUser != "" {
			handler["auther"] = "main-auth"
		}

	case "socks5", "":
		handler["type"] = "socks5"
		if node.ProxyUser != "" {
			handler["auther"] = "main-auth"
		}
		handler["metadata"] = map[string]interface{}{
			"bind":          true,
			"udp":           true,
			"udpBufferSize": 4096,
		}

	case "ss":
		handler["type"] = "ss"
		handler["auth"] = map[string]string{
			"username": node.SSMethod,
			"password": node.SSPassword,
		}

	case "trojan":
		handler["type"] = "trojan"
		handler["auth"] = map[string]string{
			"password": node.TrojanPassword,
		}

	case "vmess":
		handler["type"] = "vmess"
		handler["auth"] = map[string]string{
			"username": node.VMessUUID,
		}
		handler["metadata"] = map[string]interface{}{
			"alterID": node.VMessAlterID,
		}

	case "relay":
		handler["type"] = "relay"

	case "tcp":
		handler["type"] = "tcp"

	case "udp":
		handler["type"] = "udp"

	case "sni":
		handler["type"] = "sni"

	case "dns":
		handler["type"] = "dns"

	case "sshd":
		handler["type"] = "sshd"
		if node.ProxyUser != "" {
			handler["auther"] = "main-auth"
		}

	case "redirect":
		handler["type"] = "redirect"

	case "tun":
		handler["type"] = "tun"
		handler["metadata"] = map[string]interface{}{
			"net": "198.18.0.0/15",
		}
	}

	// DNS 解析器
	if node.DNSServer != "" {
		handler["resolver"] = "custom-resolver"
	}

	return handler
}

// generateListener 生成 Listener 配置
func (g *ConfigGenerator) generateListener(node *model.Node) map[string]interface{} {
	listener := map[string]interface{}{}

	// 某些协议的 listener 类型由协议决定，而非 transport
	switch node.Protocol {
	case "sshd":
		listener["type"] = "sshd"
		return listener
	case "redirect":
		listener["type"] = "redirect"
		return listener
	case "tun":
		listener["type"] = "tun"
		return listener
	}

	switch node.Transport {
	case "tcp", "", "tcp+udp":
		listener["type"] = "tcp"

	case "tls":
		listener["type"] = "tls"
		listener["tls"] = g.generateTLSConfig(node)

	case "mtls":
		listener["type"] = "mtls"
		listener["tls"] = g.generateTLSConfig(node)

	case "ws":
		listener["type"] = "ws"
		if node.WSPath != "" || node.WSHost != "" {
			metadata := map[string]interface{}{}
			if node.WSPath != "" {
				metadata["path"] = node.WSPath
			}
			if node.WSHost != "" {
				metadata["host"] = node.WSHost
			}
			listener["metadata"] = metadata
		}

	case "wss":
		listener["type"] = "wss"
		listener["tls"] = g.generateTLSConfig(node)
		if node.WSPath != "" || node.WSHost != "" {
			metadata := map[string]interface{}{}
			if node.WSPath != "" {
				metadata["path"] = node.WSPath
			}
			if node.WSHost != "" {
				metadata["host"] = node.WSHost
			}
			listener["metadata"] = metadata
		}

	case "h2":
		listener["type"] = "h2"
		listener["tls"] = g.generateTLSConfig(node)

	case "h2c":
		listener["type"] = "h2c"

	case "quic":
		listener["type"] = "quic"
		listener["tls"] = g.generateTLSConfig(node)

	case "kcp":
		listener["type"] = "kcp"
		// KCP 配置: 优先使用 TransportOpts，否则用默认值
		kcpConfig := map[string]interface{}{
			"mtu":        1350,
			"sndwnd":     1024,
			"rcvwnd":     1024,
			"datashard":  10,
			"parityshard": 3,
			"nodelay":    1,
			"interval":   20,
			"resend":     2,
			"nc":         1,
			"smuxver":    1,
			"smuxbuf":    4194304,
			"streambuf":  2097152,
			"keepalive":  10,
			"snmpperiod": 60,
			"tcp":        false,
		}
		if opts := g.parseTransportOpts(node); opts != nil {
			if kcp, ok := opts["kcp"].(map[string]interface{}); ok {
				for k, v := range kcp {
					kcpConfig[k] = v
				}
			}
		}
		listener["metadata"] = map[string]interface{}{"kcp": kcpConfig}

	case "grpc":
		listener["type"] = "grpc"
		listener["tls"] = g.generateTLSConfig(node)

	case "pht":
		listener["type"] = "pht"

	case "phts":
		listener["type"] = "phts"
		listener["tls"] = g.generateTLSConfig(node)

	case "ssh":
		listener["type"] = "ssh"

	case "sshd":
		listener["type"] = "sshd"

	case "mws":
		listener["type"] = "mws"
		if node.WSPath != "" || node.WSHost != "" {
			metadata := map[string]interface{}{}
			if node.WSPath != "" {
				metadata["path"] = node.WSPath
			}
			if node.WSHost != "" {
				metadata["host"] = node.WSHost
			}
			listener["metadata"] = metadata
		}

	case "mwss":
		listener["type"] = "mwss"
		listener["tls"] = g.generateTLSConfig(node)
		if node.WSPath != "" || node.WSHost != "" {
			metadata := map[string]interface{}{}
			if node.WSPath != "" {
				metadata["path"] = node.WSPath
			}
			if node.WSHost != "" {
				metadata["host"] = node.WSHost
			}
			listener["metadata"] = metadata
		}

	case "http3":
		listener["type"] = "http3"
		listener["tls"] = g.generateTLSConfig(node)

	case "dtls":
		listener["type"] = "dtls"
		listener["tls"] = g.generateTLSConfig(node)

	case "ohttp":
		listener["type"] = "ohttp"

	case "otls":
		listener["type"] = "otls"

	case "redirect":
		listener["type"] = "redirect"

	case "tun":
		listener["type"] = "tun"
	}

	return listener
}

// parseTransportOpts 解析 TransportOpts JSON
func (g *ConfigGenerator) parseTransportOpts(node *model.Node) map[string]interface{} {
	if node.TransportOpts == "" {
		return nil
	}
	var opts map[string]interface{}
	if err := json.Unmarshal([]byte(node.TransportOpts), &opts); err != nil {
		return nil
	}
	return opts
}

// generateTLSConfig 生成 TLS 配置
func (g *ConfigGenerator) generateTLSConfig(node *model.Node) map[string]interface{} {
	tls := map[string]interface{}{}

	if node.TLSCertFile != "" {
		tls["certFile"] = node.TLSCertFile
	}
	if node.TLSKeyFile != "" {
		tls["keyFile"] = node.TLSKeyFile
	}
	if node.TLSSNI != "" {
		tls["serverName"] = node.TLSSNI
	}
	if node.TLSALPN != "" {
		alpnList := []string{}
		for _, a := range strings.Split(node.TLSALPN, ",") {
			if trimmed := strings.TrimSpace(a); trimmed != "" {
				alpnList = append(alpnList, trimmed)
			}
		}
		if len(alpnList) > 0 {
			tls["alpn"] = alpnList
		}
	}

	return tls
}

// generateAuthers 生成认证器配置
func (g *ConfigGenerator) generateAuthers(node *model.Node) []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name": "main-auth",
			"auths": []map[string]string{
				{"username": node.ProxyUser, "password": node.ProxyPass},
			},
		},
	}
}

// generateLimiters 生成限速器配置
func (g *ConfigGenerator) generateLimiters(node *model.Node) []map[string]interface{} {
	if node.SpeedLimit <= 0 {
		return nil
	}

	// 转换为合适的单位
	limit := fmt.Sprintf("%dB", node.SpeedLimit)
	if node.SpeedLimit >= 1024*1024*1024 {
		limit = fmt.Sprintf("%.2fGB", float64(node.SpeedLimit)/(1024*1024*1024))
	} else if node.SpeedLimit >= 1024*1024 {
		limit = fmt.Sprintf("%.2fMB", float64(node.SpeedLimit)/(1024*1024))
	} else if node.SpeedLimit >= 1024 {
		limit = fmt.Sprintf("%.2fKB", float64(node.SpeedLimit)/1024)
	}

	return []map[string]interface{}{
		{
			"name": "speed-limiter",
			"limits": []string{
				"$ " + limit,
			},
		},
	}
}

// generateRateLimiters 生成连接速率限制器配置
func (g *ConfigGenerator) generateRateLimiters(node *model.Node) []map[string]interface{} {
	if node.ConnRateLimit <= 0 {
		return nil
	}

	return []map[string]interface{}{
		{
			"name": "rate-limiter",
			"limits": []string{
				fmt.Sprintf("$ %d/s", node.ConnRateLimit),
			},
		},
	}
}

// generateResolvers 生成 DNS 解析器配置
func (g *ConfigGenerator) generateResolvers(node *model.Node) []map[string]interface{} {
	if node.DNSServer == "" {
		return nil
	}

	return []map[string]interface{}{
		{
			"name": "custom-resolver",
			"nameservers": []map[string]interface{}{
				{
					"addr":   node.DNSServer,
					"prefer": "ipv4",
				},
			},
		},
	}
}

// GeneratePortForwardConfig 生成端口转发配置
func (g *ConfigGenerator) GeneratePortForwardConfig(pf *model.PortForward) map[string]interface{} {
	service := map[string]interface{}{
		"name": pf.Name,
		"addr": pf.LocalAddr,
		"handler": map[string]interface{}{
			"type": pf.Type,
		},
		"listener": map[string]interface{}{
			"type": pf.Type,
		},
		"forwarder": map[string]interface{}{
			"nodes": []map[string]interface{}{
				{"name": "target", "addr": pf.RemoteAddr},
			},
		},
	}

	return service
}

// GenerateChainConfig 生成转发链配置 (用于负载均衡)
func (g *ConfigGenerator) GenerateChainConfig(group *model.NodeGroup, members []NodeMemberWithNode) map[string]interface{} {
	nodes := make([]map[string]interface{}, 0, len(members))

	for _, m := range members {
		if !m.Member.Enabled {
			continue
		}

		node := m.Node
		nodeConfig := map[string]interface{}{
			"name": fmt.Sprintf("node-%d", node.ID),
			"addr": fmt.Sprintf("%s:%d", node.Host, node.Port),
		}

		// Connector 配置
		connector := map[string]interface{}{
			"type": node.Protocol,
		}
		if node.ProxyUser != "" {
			connector["auth"] = map[string]string{
				"username": node.ProxyUser,
				"password": node.ProxyPass,
			}
		}
		nodeConfig["connector"] = connector

		// Dialer 配置
		dialer := map[string]interface{}{
			"type": normalizeTransport(node.Transport),
		}
		if node.TLSEnabled {
			dialer["tls"] = g.generateTLSConfig(node)
		}
		nodeConfig["dialer"] = dialer

		// 权重
		if m.Member.Weight > 0 {
			nodeConfig["metadata"] = map[string]interface{}{
				"weight": m.Member.Weight,
			}
		}

		nodes = append(nodes, nodeConfig)
	}

	chain := map[string]interface{}{
		"name": fmt.Sprintf("chain-%d", group.ID),
		"hops": []map[string]interface{}{
			{
				"name":  "hop-0",
				"nodes": nodes,
				"selector": map[string]interface{}{
					"strategy":    group.Strategy,
					"maxFails":    group.MaxFails,
					"failTimeout": fmt.Sprintf("%ds", group.FailTimeout),
				},
			},
		},
	}

	return chain
}

// NodeMemberWithNode 节点组成员及其节点信息
type NodeMemberWithNode struct {
	Member model.NodeGroupMember
	Node   *model.Node
}

// GenerateProxyChainConfig 生成代理链配置 (多跳隧道)
func (g *ConfigGenerator) GenerateProxyChainConfig(chain *model.ProxyChain, hops []model.ProxyChainHop) map[string]interface{} {
	// 生成多跳转发链
	hopConfigs := make([]map[string]interface{}, 0, len(hops))

	for i, hop := range hops {
		if !hop.Enabled || hop.Node == nil {
			continue
		}

		node := hop.Node
		nodeConfig := map[string]interface{}{
			"name": fmt.Sprintf("node-%d", node.ID),
			"addr": fmt.Sprintf("%s:%d", node.Host, node.Port),
		}

		// Connector 配置
		connector := map[string]interface{}{
			"type": node.Protocol,
		}
		if node.ProxyUser != "" {
			connector["auth"] = map[string]string{
				"username": node.ProxyUser,
				"password": node.ProxyPass,
			}
		}
		nodeConfig["connector"] = connector

		// Dialer 配置
		dialer := map[string]interface{}{
			"type": node.Transport,
		}
		if node.TLSEnabled {
			dialer["tls"] = g.generateTLSConfig(node)
		}
		nodeConfig["dialer"] = dialer

		hopConfig := map[string]interface{}{
			"name":  fmt.Sprintf("hop-%d", i),
			"nodes": []map[string]interface{}{nodeConfig},
		}

		hopConfigs = append(hopConfigs, hopConfig)
	}

	chainConfig := map[string]interface{}{
		"name": fmt.Sprintf("tunnel-%d", chain.ID),
		"hops": hopConfigs,
	}

	return chainConfig
}

// GenerateProxyChainFullConfig 生成完整的代理链服务配置
func (g *ConfigGenerator) GenerateProxyChainFullConfig(chain *model.ProxyChain, hops []model.ProxyChainHop) map[string]interface{} {
	chainName := fmt.Sprintf("tunnel-%d", chain.ID)

	// 生成转发链配置
	chainConfig := g.GenerateProxyChainConfig(chain, hops)

	// 生成服务配置
	service := map[string]interface{}{
		"name": fmt.Sprintf("tunnel-service-%d", chain.ID),
		"addr": chain.ListenAddr,
		"handler": map[string]interface{}{
			"type":  chain.ListenType,
			"chain": chainName,
		},
		"listener": map[string]interface{}{
			"type": "tcp",
		},
	}

	// 如果有目标地址，添加 forwarder (用于端口转发)
	if chain.TargetAddr != "" {
		service["forwarder"] = map[string]interface{}{
			"nodes": []map[string]interface{}{
				{"name": "target", "addr": chain.TargetAddr},
			},
		}
		// 端口转发时使用 tcp handler
		service["handler"] = map[string]interface{}{
			"type":  "tcp",
			"chain": chainName,
		}
	}

	config := map[string]interface{}{
		"services": []map[string]interface{}{service},
		"chains":   []map[string]interface{}{chainConfig},
	}

	return config
}

// GenerateTunnelEntryConfig 生成隧道入口端配置 (部署在入口节点)
// 支持端口复用：tcp+udp 模式下同一端口同时监听 TCP 和 UDP
func (g *ConfigGenerator) GenerateTunnelEntryConfig(tunnel *model.Tunnel) map[string]interface{} {
	exitNode := tunnel.ExitNode
	if exitNode == nil {
		return nil
	}

	chainName := fmt.Sprintf("tunnel-chain-%d", tunnel.ID)

	// 转发链配置 - 连接到出口节点
	connector := map[string]interface{}{
		"type": exitNode.Protocol,
	}
	if exitNode.ProxyUser != "" {
		connector["auth"] = map[string]string{
			"username": exitNode.ProxyUser,
			"password": exitNode.ProxyPass,
		}
	}

	dialer := map[string]interface{}{
		"type": normalizeTransport(exitNode.Transport),
	}
	if exitNode.TLSEnabled {
		dialer["tls"] = g.generateTLSConfig(exitNode)
	}

	chain := map[string]interface{}{
		"name": chainName,
		"hops": []map[string]interface{}{
			{
				"name": "hop-0",
				"nodes": []map[string]interface{}{
					{
						"name":      fmt.Sprintf("exit-%d", exitNode.ID),
						"addr":      fmt.Sprintf("%s:%d", exitNode.Host, exitNode.Port),
						"connector": connector,
						"dialer":    dialer,
					},
				},
			},
		},
	}

	// 生成服务列表 - 支持端口复用 (tcp+udp)
	services := []map[string]interface{}{}
	protocols := g.parseProtocols(tunnel.Protocol)

	for _, proto := range protocols {
		service := map[string]interface{}{
			"name": fmt.Sprintf("tunnel-%d-%s", tunnel.ID, proto),
			"addr": fmt.Sprintf(":%d", tunnel.EntryPort),
			"handler": map[string]interface{}{
				"type":  proto,
				"chain": chainName,
			},
			"listener": map[string]interface{}{
				"type": proto,
			},
		}

		// 如果有目标地址，添加 forwarder
		if tunnel.TargetAddr != "" {
			service["forwarder"] = map[string]interface{}{
				"nodes": []map[string]interface{}{
					{"name": "target", "addr": tunnel.TargetAddr},
				},
			}
		}

		// 限速配置
		if tunnel.SpeedLimit > 0 {
			service["limiter"] = "tunnel-limiter"
		}

		services = append(services, service)
	}

	config := map[string]interface{}{
		"services": services,
		"chains":   []map[string]interface{}{chain},
	}

	// 添加限速器
	if tunnel.SpeedLimit > 0 {
		limit := fmt.Sprintf("%dB", tunnel.SpeedLimit)
		if tunnel.SpeedLimit >= 1024*1024*1024 {
			limit = fmt.Sprintf("%.2fGB", float64(tunnel.SpeedLimit)/(1024*1024*1024))
		} else if tunnel.SpeedLimit >= 1024*1024 {
			limit = fmt.Sprintf("%.2fMB", float64(tunnel.SpeedLimit)/(1024*1024))
		} else if tunnel.SpeedLimit >= 1024 {
			limit = fmt.Sprintf("%.2fKB", float64(tunnel.SpeedLimit)/1024)
		}
		config["limiters"] = []map[string]interface{}{
			{
				"name":   "tunnel-limiter",
				"limits": []string{"$ " + limit},
			},
		}
	}

	return config
}

// parseProtocols 解析协议字符串，支持 tcp+udp 格式
func (g *ConfigGenerator) parseProtocols(protocol string) []string {
	switch protocol {
	case "tcp+udp", "udp+tcp":
		return []string{"tcp", "udp"}
	case "udp":
		return []string{"udp"}
	case "tcp", "":
		return []string{"tcp"}
	default:
		return []string{"tcp"}
	}
}

// normalizeTransport 标准化传输层协议 (tcp+udp -> tcp)
func normalizeTransport(transport string) string {
	switch transport {
	case "tcp+udp", "udp+tcp":
		return "tcp"
	case "":
		return "tcp"
	default:
		return transport
	}
}

// GenerateTunnelExitConfig 生成隧道出口端配置 (部署在出口节点)
// 出口节点使用标准节点配置即可
func (g *ConfigGenerator) GenerateTunnelExitConfig(tunnel *model.Tunnel) map[string]interface{} {
	if tunnel.ExitNode == nil {
		return nil
	}
	return g.GenerateNodeConfig(tunnel.ExitNode)
}

// ==================== Bypass/Admission/Hosts 配置生成 ====================

// generateBypassConfigs 生成 Bypass 分流规则配置
func (g *ConfigGenerator) generateBypassConfigs(nodeID uint, bypasses []model.Bypass) []map[string]interface{} {
	// 合并所有 bypass 规则到一个配置中
	allMatchers := []string{}
	whitelist := false

	for _, b := range bypasses {
		if b.Whitelist {
			whitelist = true
		}
		var matchers []string
		if err := json.Unmarshal([]byte(b.Matchers), &matchers); err == nil {
			allMatchers = append(allMatchers, matchers...)
		}
	}

	if len(allMatchers) == 0 {
		return nil
	}

	return []map[string]interface{}{
		{
			"name":      fmt.Sprintf("bypass-%d", nodeID),
			"whitelist": whitelist,
			"matchers":  allMatchers,
		},
	}
}

// generateAdmissionConfigs 生成 Admission 准入控制配置
func (g *ConfigGenerator) generateAdmissionConfigs(nodeID uint, admissions []model.Admission) []map[string]interface{} {
	allMatchers := []string{}
	whitelist := false

	for _, a := range admissions {
		if a.Whitelist {
			whitelist = true
		}
		var matchers []string
		if err := json.Unmarshal([]byte(a.Matchers), &matchers); err == nil {
			allMatchers = append(allMatchers, matchers...)
		}
	}

	if len(allMatchers) == 0 {
		return nil
	}

	return []map[string]interface{}{
		{
			"name":      fmt.Sprintf("admission-%d", nodeID),
			"whitelist": whitelist,
			"matchers":  allMatchers,
		},
	}
}

// generateHostsConfigs 生成 Hosts 主机映射配置
func (g *ConfigGenerator) generateHostsConfigs(nodeID uint, hostMappings []model.HostMapping) []map[string]interface{} {
	type hostEntry struct {
		Hostname string `json:"hostname"`
		IP       string `json:"ip"`
		Prefer   string `json:"prefer,omitempty"`
	}

	allMappings := []map[string]interface{}{}

	for _, hm := range hostMappings {
		var entries []hostEntry
		if err := json.Unmarshal([]byte(hm.Mappings), &entries); err == nil {
			for _, e := range entries {
				mapping := map[string]interface{}{
					"ip":       e.IP,
					"hostname": e.Hostname,
				}
				if e.Prefer != "" {
					mapping["prefer"] = e.Prefer
				}
				allMappings = append(allMappings, mapping)
			}
		}
	}

	if len(allMappings) == 0 {
		return nil
	}

	return []map[string]interface{}{
		{
			"name":     fmt.Sprintf("hosts-%d", nodeID),
			"mappings": allMappings,
		},
	}
}

// generateIngressConfigs 生成 Ingress 反向代理配置
func (g *ConfigGenerator) generateIngressConfigs(nodeID uint, ingresses []model.Ingress) []map[string]interface{} {
	type ingressRule struct {
		Hostname string `json:"hostname"`
		Endpoint string `json:"endpoint"`
	}

	allRules := []map[string]interface{}{}

	for _, ing := range ingresses {
		var rules []ingressRule
		if err := json.Unmarshal([]byte(ing.Rules), &rules); err == nil {
			for _, r := range rules {
				rule := map[string]interface{}{
					"hostname": r.Hostname,
					"endpoint": r.Endpoint,
				}
				allRules = append(allRules, rule)
			}
		}
	}

	if len(allRules) == 0 {
		return nil
	}

	return []map[string]interface{}{
		{
			"name":  fmt.Sprintf("ingress-%d", nodeID),
			"rules": allRules,
		},
	}
}

// generateRouterConfigs 生成 Router 路由配置
func (g *ConfigGenerator) generateRouterConfigs(nodeID uint, routers []model.Router) []map[string]interface{} {
	type routeEntry struct {
		Net     string `json:"net"`
		Gateway string `json:"gateway"`
	}

	allRoutes := []map[string]interface{}{}

	for _, r := range routers {
		var routes []routeEntry
		if err := json.Unmarshal([]byte(r.Routes), &routes); err == nil {
			for _, rt := range routes {
				route := map[string]interface{}{
					"net":     rt.Net,
					"gateway": rt.Gateway,
				}
				allRoutes = append(allRoutes, route)
			}
		}
	}

	if len(allRoutes) == 0 {
		return nil
	}

	return []map[string]interface{}{
		{
			"name":   fmt.Sprintf("router-%d", nodeID),
			"routes": allRoutes,
		},
	}
}
