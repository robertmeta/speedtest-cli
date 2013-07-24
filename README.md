# speedtest-cli

Command line interface for testing internet bandwidth using speedtest.net


## Versions

speedtest-cli is written in Go and you can choose to just get the binaries
from the bin directory if you want a version that will just work for 
your platform.


## Usage

    $ speedtest-cli -help
    usage: speedtest-cli [-h] [-share] [-simple] [-list] [-server SERVER]

    Command line interface for testing internet bandwidth using speedtest.net.
    --------------------------------------------------------------------------
    https://github.com/sivel/speedtest-cli

    optional arguments:
      --help          Show this help message and exit
      -share          Generate and provide a URL to the speedtest.net share
                      results image
      -simple         Suppress verbose output, only show basic information
      -list           Display a list of speedtest.net servers sorted by distance
      -server SERVER  Specify a server ID to test against
