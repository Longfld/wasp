package dashboard

import (
	"html/template"
	"net"
	"os"
	"time"

	"github.com/iotaledger/wasp/tools/wwallet/config"
	"github.com/labstack/echo"
)

type BaseTemplateParams struct {
	Host       string
	NavPages   []NavPage
	ActivePage string
}

var navPages = []NavPage{}

func BaseParams(c echo.Context, page string) BaseTemplateParams {
	host, _, err := net.SplitHostPort(c.Request().Host)
	if err != nil {
		panic(err)
	}
	return BaseTemplateParams{Host: host, NavPages: navPages, ActivePage: page}
}

type NavPage struct {
	Title string
	Href  string
}

func MakeTemplate(parts ...string) *template.Template {
	t := template.New("").Funcs(template.FuncMap{
		"formatTimestamp": func(ts interface{}) string {
			t, ok := ts.(time.Time)
			if !ok {
				t = time.Unix(0, ts.(int64)).UTC()
			}
			return t.Format(time.RFC3339)
		},
		"waspClientCmd": func() string {
			if config.Utxodb {
				return os.Args[0] + " -u"
			}
			return os.Args[0]
		},
	})
	t = template.Must(t.Parse(tplBase))
	for _, part := range parts {
		t = template.Must(t.Parse(part))
	}
	return t
}

const tplBase = `
{{define "base"}}
	<!doctype html>
	<html lang="en">
	<head>
		<meta charset="utf-8" />
		<meta http-equiv="x-ua-compatible" content="ie=edge" />
		<meta name="viewport" content="width=device-width, initial-scale=1" />

		<title>{{template "title"}} - IOTA Smart Contracts PoC</title>

		<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/@xz/fonts@1/serve/inter.css">
		<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/@exampledev/new.css@1.1.2/new.min.css">
	</head>

	<body>
		<style>
			details {background: #EEF9FF}
		</style>
		<header>
			<h1>IOTA Smart Contracts PoC</h1>
			<nav>
				{{$activePage := .ActivePage}}
				{{range $i, $p := .NavPages}}
					{{if $i}} | {{end}}
					{{if eq $activePage $p.Href}}
						<strong>{{$p.Title}}</strong>
					{{else}}
						<a href="{{$p.Href}}">{{$p.Title}}</a>
					{{end}}
				{{end}}
			</nav>
		</header>
		{{template "body" .}}
	</body>
	</html>
{{end}}`

const TplSCInfo = `
{{define "sc-info"}}
	<details>
		<summary>Smart contract details</summary>
		<p>SC address: <code>{{.Status.SCAddress}}</code></p>
		<p>Program hash: <code>{{.Status.ProgramHash}}</code></p>
		<p>Description of the instance: <code>{{.Status.Description}}</code></p>
		<p>Owner address: <code>{{.Status.OwnerAddress}}</code></p>
		<p>Minimum node reward (fee): <code>{{.Status.MinimumReward}}</code></p>
	</details>
	<details>
		<summary>State details</summary>
		<p>
          <ul>
		    <li>Index: <code>{{.Status.StateIndex}}</code></li>
		    <li>Timestamp: <code>{{.Status.Timestamp}}</code></li>
		    <li>Anchor transaction: <code>{{.Status.StateTxId}}</code></li>
		    <li>State hash: <code>{{.Status.StateHash}}</code></li>
			<li>Requests in the batch:</li>
		    <ul>
	            {{range $_, $reqid := .Status.Requests}}
			       <li><code>{{$reqid}}</code></li>
		        {{end}}
		    </ul>
		  </ul>
		</p>

		<p>Balance: <ul>
		{{range $color, $amount := .Status.Balance}}
			<li><code>{{$color}}</code>: <code>{{$amount}} </code></li>
		{{end}}
		</ul></p>
	</details>
{{end}}`

const TplWs = `
{{define "ws"}}
	<script>
		const url = 'ws://' +  location.host + '/ws/{{.Config.ShortName}}';
		console.log('opening WebSocket to ' + url);
		const ws = new WebSocket(url);

		ws.addEventListener('error', function (event) {
			console.error('WebSocket error!', event);
		});

		const connectedAt = new Date();
		ws.addEventListener('message', function (event) {
			console.log('Message from server: ', event.data);
			ws.close();
			if (new Date() - connectedAt > 5000) {
				location.reload();
			} else {
				setTimeout(() => location.reload(), 5000);
			}
		});
	</script>
{{end}}
`

const TplInstallConfig = `
{{define "install-config"}}
	<details>
		<summary>1. Install</summary>
		<p>Grab the latest <code>wwallet</code> binary from the
		<a href="https://github.com/iotaledger/wasp/releases">Releases</a> page.</p>
		<p>-- OR --</p>
		<p>Build from source:</p>
<pre>$ git clone --branch develop https://github.com/iotaledger/wasp.git
$ cd wasp
$ go install ./tools/wwallet
</pre>
	</details>
	<details>
		<summary>2. Configure</summary>
<pre>$ {{waspClientCmd}} set goshimmer.api {{.Host}}:8080
$ {{waspClientCmd}} set wasp.api {{.Host}}:9090
$ {{waspClientCmd}} {{.Config.ShortName}} set address {{.Config.Address}}</pre>
		<p>Initialize a wallet: <code>{{waspClientCmd}} init</code></p>
		<p>Get some funds: <code>{{waspClientCmd}} request-funds</code></p>
	</details>
{{end}}
`
