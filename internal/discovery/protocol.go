package discovery

var portProtocols = map[int]string{
	21: "ftp", 22: "ssh", 25: "smtp", 53: "dns", 80: "http", 443: "https",
	465: "smtp", 587: "smtp", 1433: "tcp", 1883: "mqtt", 2375: "http",
	2376: "https", 3000: "http", 3306: "tcp", 5000: "http", 5432: "tcp",
	5672: "tcp", 6379: "tcp", 6443: "https", 8080: "http", 8443: "https",
	8500: "http", 8600: "dns", 8883: "mqtt", 9200: "http", 9300: "http",
	11211: "tcp", 15672: "http", 27017: "tcp",
}

var portNames = map[int]string{
	21: "FTP", 22: "SSH Server", 25: "SMTP", 53: "DNS", 80: "HTTP", 443: "HTTPS",
	465: "SMTP (SSL)", 587: "SMTP (submission)", 1433: "MSSQL", 1883: "MQTT",
	2375: "Docker API", 2376: "Docker API (TLS)", 3000: "HTTP", 3306: "MySQL",
	5000: "HTTP", 5432: "PostgreSQL", 5672: "RabbitMQ", 6379: "Redis",
	6443: "Kubernetes API", 8080: "HTTP", 8443: "HTTPS", 8500: "Consul",
	8600: "Consul DNS", 8883: "MQTT (TLS)", 9200: "Elasticsearch",
	9300: "Elasticsearch Cluster", 11211: "Memcached",
	15672: "RabbitMQ Management", 27017: "MongoDB",
}

func detectProtocol(port int) string {
	if protocol, ok := portProtocols[port]; ok {
		return protocol
	}
	return "tcp"
}

func isHTTPProtocol(protocol string) bool {
	return protocol == "http" || protocol == "https"
}

func networkProtocol(port int) string {
	protocol := detectProtocol(port)
	if isHTTPProtocol(protocol) || protocol == "ssh" {
		return protocol
	}
	return "tcp"
}
