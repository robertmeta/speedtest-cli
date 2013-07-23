package main

import (
	"encoding/xml"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"time"
)

const (
	configUrl              = "http://www.speedtest.net/speedtest-config.php"
	degToRad               = math.Pi / 180
	earthsRadiusKm         = 6371
	helpFlag               = "help"
	helpHelp               = "Show this help message and exit"
	latencyPath            = "/latency.txt"
	listFlag               = "list"
	listHelp               = "Display a list of speedtest.net servers sorted by distance"
	nanoSecPerMilli        = 1000000
	numberOfClosestServers = 5
	serverFlag             = "server"
	serverHelp             = "Specify a server ID to test against"
	serversUrl             = "http://www.speedtest.net/speedtest-servers.php"
	shareFlag              = "share"
	shareHelp              = "Generate and provide a URL to the speedtest.net share results image"
	simpleFlag             = "simple"
	simpleHelp             = "Suppress verbose output, only show basic information"
	timesToRunLatency      = 5
)

var (
	help   bool
	share  bool
	simple bool
	list   bool
	server string
	// TODO: This is a hacky const-alike for the download sizes, do better
	downloadSizes = [...]int64{350, 500, 750, 1000, 1500, 2000, 2500, 3000, 3500, 4000}
)

type Point struct {
	Lat  float64
	Long float64
}

type Client struct {
	Ip   string  `xml:"ip,attr"`
	Isp  string  `xml:"isp,attr"`
	Lat  float64 `xml:"lat,attr"`
	Long float64 `xml:"lon,attr"`
}

type Config struct {
	XMLName    xml.Name `xml:"settings"`
	LicenseKey string   `xml:"licensekey"`
	Clients    []Client `xml:"client"`
	Times      []struct {
		Dl1 string `xml:"dl1,attr"`
	} `xml:"times"`
	Download []struct {
		TestLength string `xml:"testlength,attr"`
	} `xml:"download"`
	Upload []struct {
		TestLength string `xml:"testlength,attr"`
	} `xml:"upload"`
}

type Server struct {
	Name     string  `xml:"name,attr"`
	Sponsor  string  `xml:"sponsor,attr"`
	Country  string  `xml:"country,attr"`
	Lat      float64 `xml:"lat,attr"`
	Long     float64 `xml:"lon,attr"`
	Url      string  `xml:"url,attr"`
	Host     string  `xml:"host,attr"`
	Distance float64
	Ping     int64
}

type Servers struct {
	XMLName     xml.Name `xml:"settings"`
	ServerGroup []struct {
		Servers []Server `xml:"server"`
	} `xml:"servers"`
}

func init() {
	flag.BoolVar(&help, "help", false, helpHelp)
	flag.BoolVar(&share, "share", false, shareHelp)
	flag.BoolVar(&simple, "simple", false, simpleHelp)
	flag.BoolVar(&list, "list", false, listHelp)
	flag.StringVar(&server, "server", "", serverHelp)
}

func main() {
	flag.Parse()
	if help {
		usage()
		os.Exit(2)
	}
	c := getConfig()
	client := c.Clients[0]
	s := getClosestServers(client)
	if simple != true {
		fmt.Printf("Testing from %s (%s)...\n", client.Isp, client.Ip)
	}
	b := getBestServer(s)
	if simple != true {
		fmt.Printf("Hosted by %s (%s) [%f km] %d ms\n", b.Sponsor, b.Name, b.Distance, b.Ping)
	}
}

func getConfig() Config {
	if simple != true {
		fmt.Printf("Retrieving speedtest.net configuration...\n")
	}
	configXml, _ := fetchHttp(configUrl)
	config := Config{}
	xml.Unmarshal(configXml, &config)
	return config
}

// TODO: Fugly use of a map and an array, need to clean it up
func getClosestServers(client Client) []Server {
	if simple != true {
		fmt.Printf("Retrieving speedtest.net server list ...\n")
	}
	serversXml, _ := fetchHttp(serversUrl)
	servers := Servers{}
	xml.Unmarshal(serversXml, &servers)

	closestServers := make(map[float64]Server)

	for _, server := range servers.ServerGroup[0].Servers {
		server.Distance = distance(Point{client.Lat, client.Long}, Point{server.Lat, server.Long})
		if len(closestServers) < 5 {
			closestServers[server.Distance] = server
		} else {
			var highestDistance float64
			for k := range closestServers {
				if k > highestDistance {
					highestDistance = k
				}
			}
			if server.Distance < highestDistance {
				delete(closestServers, highestDistance)
				closestServers[server.Distance] = server
			}
		}
	}

	returnServers := make([]Server, 0)
	for _, v := range closestServers {
		returnServers = append(returnServers, v)
	}

	return returnServers
}

func getBestServer(servers []Server) Server {
	if simple != true {
		fmt.Printf("Selecting best server based on ping...\n")
	}
	firstPass := true
	var bestServer Server
	for _, server := range servers {
		u, err := url.Parse(server.Url)
		if err != nil {
			log.Fatal(err)
		}
		u.Path = latencyPath
		totalDur := time.Since(time.Now())
		for i := 0; i < timesToRunLatency; i++ {
			_, dur := fetchHttp(u.String())
			totalDur += dur
		}
		server.Ping = durationToMilliSeconds(totalDur) / timesToRunLatency
		if firstPass || server.Ping < bestServer.Ping {
			firstPass = false
			bestServer = server
		}
	}
	return bestServer
}

func durationToMilliSeconds(td time.Duration) int64 {
	return int64(td.Nanoseconds() / nanoSecPerMilli)
}

func fetchHttp(url string) ([]byte, time.Duration) {
	startTime := time.Now()
	res, err := http.Get(url)
	if err != nil {
		log.Fatal(err)
	}
	defer res.Body.Close()
	body, err := ioutil.ReadAll(res.Body)
	endTime := time.Now()
	downloadTime := endTime.Sub(startTime)
	if err != nil {
		log.Fatal(err)
	}
	return body, downloadTime
}

func distance(origin Point, destination Point) float64 {
	dlat := (destination.Lat - origin.Lat) * degToRad
	dlon := (destination.Long - origin.Long) * degToRad
	radOriginLat := origin.Lat * degToRad
	a := (math.Sin(dlat/2)*math.Sin(dlat/2) +
		math.Cos(radOriginLat)*math.Cos(radOriginLat)*math.Sin(dlon/2)*math.Sin(dlon/2))
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	d := earthsRadiusKm * c
	return d
}

func usage() {
	fmt.Printf("usage: %s [-%s] [-%s] ", os.Args[0], helpFlag, shareFlag)
	fmt.Printf("[-%s] [-%s] [-%s SERVER]\n\n", simpleFlag, listFlag, serverFlag)
	fmt.Printf("Command line interface for testing internet bandwidth using speedtest.net.\n")
	fmt.Printf("--------------------------------------------------------------------------\n")
	fmt.Printf("https://github.com/robertmeta/speedtest-cli\n")
	fmt.Printf(" a port of\n")
	fmt.Printf("https://github.com/sivel/speedtest-cli\n\n")
	fmt.Printf("optional arguments\n")
	fmt.Printf("\t-%s\t\t\t%s\n", helpFlag, helpHelp)
	fmt.Printf("\t-%s\t\t\t%s\n", shareFlag, shareHelp)
	fmt.Printf("\t-%s\t\t\t%s\n", simpleFlag, simpleHelp)
	fmt.Printf("\t-%s\t\t\t%s\n", listFlag, listHelp)
	fmt.Printf("\t-%s SERVER\t\t%s\n", serverFlag, serverHelp)
}
