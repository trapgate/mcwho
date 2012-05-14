#mcwho

For Minecraft servers, mcwho creates an RSS feed containing each logged on user, and how long they've been on.

##Background:

I run a small Minecraft server for friends. I also have a Logitech Squeezebox (which is a networked music player) in my living room. One day, while looking up at its fluorescent display, I decided it'd be neat if the Squeezebox could display a list of who was logged into the Minecraft server. And so mcwho was born.

##Building the Program:

mcwho is written in Go, because I wanted to try out the language. Head to http://golang.org if you need the compiler. The current version is built using Go 1.0. I'm using Google's compiler; I have not tried building it with gccgo. Also, I run my Minecraft server on Linux. I haven't tested this program on Windows or OSX. It should work, but it may not.

mcwho uses fsnotify for file change notifications. The fsnotify project is hosted on github. You'll need to execute 
 go get github.com/howeyc/fsnotify 
before you can build mcwho.

If you've set up your GOPATH correctly, just switch into src/mcwho and type: 

 go build

##Running the program

There's only one command-line switch at the moment, which is used to specify the directory that contains the Minecraft server log file. So to start mcwho:

 ./mcwho --log-path /home/minecraft/minecraft

You may also want to set mcwho up to start automatically when your server boots. If you're on Ubuntu you can use the included file mcwho.conf, which is an upstart configuration file. Edit this file if you need to, then place it in /etc/init, and type this command as root:

 initctl start mcwho

### Setting up a Squeezebox

If you happen to have a Squeezebox of your own, here's how to set it up to display the RSS feed from mcwho. Go to Settings in the Logitech Media Server. On the Player tab, change your player's "Screensaver when off" to "RSS News Ticker". Then click the Plugins tab, and click Settings next to RSS News Ticker. I removed all the existing feeds first; if you don't you'll have to wait while whatever other news there is scrolls by. Either way, in "Add new feed", enter the RSS feed URL for mcwho:

 http://myserver:9092/mcwhorss

(Change myserver to the name or IP address of your Minecraft server).

##How it Works:

Minecraft servers create a log file, server.log. mcwho runs on the Minecraft server, and parses the log file to determine who is currently logged in, and how long they've been playing. It combines all the logged in users into a single RSS feed, which it serves at this address:

 http://servername:9092/mcwhorss

mcwho uses a notification object to watch the log file for changes; when a change is detected it reads the new part of the log file and updates its RSS feed.

The RSS feed is very basic. It contains a single item, with a title that lists all the user information. This works fine with the Squeezebox, but may give other RSS readers trouble.
