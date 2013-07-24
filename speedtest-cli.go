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
	"regexp"
	"runtime"
	"strconv"
	"sync"
	"time"
)

const (
	bytesPerMegabit        = 131072
	concurrentDownloads    = 6 // not sure how the origin author picked 6
	duplicateDownloads     = 4 // how many times do we download each image
	configUrl              = "http://www.speedtest.net/speedtest-config.php"
	degToRad               = math.Pi / 180
	earthsRadiusKm         = 6371
	helpFlag               = "help"
	helpHelp               = "Show this help message and exit"
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
	wg            sync.WaitGroup
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
	Distance float64 // calculated by us
	Ping     int64   // calculated by us
}

type Servers struct {
	XMLName     xml.Name `xml:"settings"`
	ServerGroup []struct {
		Servers []Server `xml:"server"`
	} `xml:"servers"`
}

func init() {
	runtime.GOMAXPROCS(runtime.NumCPU() * 2)
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
	client := getClient(c)
	s := getClosestServers(client)
	b := getBestServer(s)
	mb := downloadSpeed(b)
	fmt.Printf("Download: %0.2f Mbit/s\n", mb)
}

func getClient(c Config) Client {
	client := c.Clients[0]
	if simple != true {
		fmt.Printf("Testing from %s (%s)...\n", client.Isp, client.Ip)
	}
	return client
}

func getConfig() Config {
	if simple != true {
		fmt.Printf("Retrieving speedtest.net configuration...\n")
	}
	configXml, _, err := fetchHttp(configUrl)
	if err != nil {
		log.Fatal(err)
	}
	config := Config{}
	xml.Unmarshal(configXml, &config)
	return config
}

// TODO: Fugly use of a map and an array, need to clean it up
func getClosestServers(client Client) []Server {
	if simple != true {
		fmt.Printf("Retrieving speedtest.net server list ...\n")
	}
	serversXml, _, err := fetchHttp(serversUrl)
	if err != nil {
		log.Fatal(err)
	}
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
	var bestServerLock sync.Mutex
	for _, server := range servers {
		wg.Add(1)
		go func(server Server) {
			defer wg.Done()
			u, err := url.Parse(server.Url)
			if err != nil {
				log.Fatal(err)
			}
			u.Path = "/latency.txt"
			totalDur := time.Since(time.Now())
			for i := 0; i < timesToRunLatency; i++ {
				_, dur, err := fetchHttp(u.String())
				if err != nil {
					fmt.Printf("Failure during getBestServer: %s\n", err.Error())
					break
				}
				totalDur += dur
			}
			server.Ping = durationToMilliSeconds(totalDur) / timesToRunLatency
			bestServerLock.Lock()
			if firstPass || server.Ping < bestServer.Ping {
				firstPass = false
				bestServer = server
			}
			bestServerLock.Unlock()
		}(server)
	}
	wg.Wait()
	return bestServer
}

func downloadSpeed(server Server) float64 {
	re := regexp.MustCompile("(.*)/(.+?)$")
	ch := make(chan string)
	totalBytes := 0.0
	totalDur := time.Since(time.Now())
	var totalBytesLock sync.Mutex

	if simple != true {
		fmt.Printf("Hosted by %s (%s) [%0.2f km] %d ms\n", server.Sponsor,
			server.Name, server.Distance, server.Ping)
	}
	u, err := url.Parse(server.Url)
	if err != nil {
		log.Fatal(err)
	}
	wg.Add(1)
	go func() { // URL Generator (producer)
		wg.Done()
		for _, size := range downloadSizes {
			for i := 0; i < duplicateDownloads; i++ {
				u.Path = re.ReplaceAllString(u.Path,
					"$1/random"+strconv.Itoa(int(size))+"x"+strconv.Itoa(int(size))+".jpg")
				ch <- u.String()
			}
		}
		close(ch)
	}()

	fmt.Printf("Testing download speed")
	startTime := time.Now()
	for i := 0; i < concurrentDownloads; i++ { // URL consumers
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if simple != true {
					fmt.Printf(".")
				}
				url, ok := <-ch
				if ok == false {
					break
				}
				b, d, _ := fetchHttp(url)
				totalBytesLock.Lock()
				totalBytes += float64(len(b))
				totalDur += d
				totalBytesLock.Unlock()
			}
		}()
	}
	wg.Wait()
	fmt.Printf("\n")
	bytesPerSecond := totalBytes / totalDur.Seconds()
	megaBitsPerSecond := bytesPerSecond / bytesPerMegabit
	return megaBitsPerSecond
}

func durationToMilliSeconds(td time.Duration) int64 {
	return int64(td.Nanoseconds() / nanoSecPerMilli)
}

func fetchHttp(url string) ([]byte, time.Duration, error) {
	startTime := time.Now()
	res, err := http.Get(url)
	if err != nil {
		return nil, time.Since(time.Now()), err
	}
	defer res.Body.Close()
	body, err := ioutil.ReadAll(res.Body)
	endTime := time.Now()
	downloadTime := endTime.Sub(startTime)
	if err != nil {
		return nil, time.Since(time.Now()), err
	}
	return body, downloadTime, nil
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
