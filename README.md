# paramvoid

Finds the HTTP parameters a web application accepts but never tells you about.

![license](https://img.shields.io/badge/license-MIT-blue?style=flat-square)
![go](https://img.shields.io/badge/go-1.21%2B-00ADD8?style=flat-square)
![release](https://img.shields.io/badge/release-v1.0.3-brightgreen?style=flat-square)

## What it does

Most web applications read more parameters than their own pages ever send. A
page might link to:

```
https://shop.example/account?id=1042
```

and the code behind it might also quietly accept `debug`, `admin`, `is_staff`,
`redirect_to` or `user_id` — none of which appear anywhere in the HTML, the
JavaScript, or the docs. Those forgotten parameters are where access-control
bugs, hidden debug modes and mass-assignment issues live, because nobody
remembers to protect an input nobody remembers exists.

paramvoid finds them. It sends a normal request first to learn what a boring
response looks like, then works through a wordlist in batches, watching for the
responses that come back *different*. A parameter the server ignores changes
nothing. A parameter the server actually reads changes something — the length,
the status, the body — and that difference is the signal.

Because it tests parameters in groups and then narrows down only the groups that
reacted, it covers a large wordlist in a fraction of the requests a
one-at-a-time scan would need.

## Why you'd use it

- **Nothing to install alongside it.** One static binary, no runtime, no
  dependency tree. The wordlist is compiled in, so it works the moment you
  build it.
- **Adapts to the target's rate limiting** instead of hammering through it —
  it slows down on 429/503 and recovers, rather than getting you blocked.
- **Resumable.** Long scans checkpoint to a file and pick up where they stopped.
- **GET, POST, JSON and XML** bodies, not just query strings.
- **Routes through Burp** so findings land in the proxy history you're already
  working from.

## Install

```bash
go install github.com/CypherNova1337/paramvoid@latest
```

Or build from a checkout:

```bash
git clone https://github.com/CypherNova1337/paramvoid
cd paramvoid
go build
```

Needs Go 1.21 or newer. Nothing else.

## Usage

Point it at a URL:

```bash
paramvoid -u https://shop.example/account
```

That's the whole first run. It uses the built-in wordlist and sensible defaults,
and prints the parameters the target reacted to.

**Test a POST body instead of the query string**

```bash
paramvoid -u https://shop.example/api/update -m POST
```

`-m` also takes `JSON` and `XML`, which send the parameters as a JSON object or
an XML document rather than form fields.

**Run a list of targets**

```bash
paramvoid -i targets.txt -oJ results.json
```

**Send everything through Burp**

```bash
paramvoid -u https://shop.example/account -oB 127.0.0.1:8080
```

**Go gently against something fragile**

```bash
paramvoid -u https://shop.example/account -rate-limit 5 -stable
```

`-stable` drops to a single worker. Slower, but it removes concurrency as a
variable when results look inconsistent.

**Resume a long scan**

```bash
paramvoid -u https://shop.example/account -state scan.state
# after an interruption
paramvoid -u https://shop.example/account -state scan.state -resume
```

**Keep a session while you scan**

```bash
paramvoid -u https://shop.example/account \
  -headers 'Cookie: session=abc123\nX-Api-Key: k1'
```

Parameters behind a login are usually the interesting ones, so this matters more
than it looks.

## Options

| Flag | Default | What it does |
|---|---|---|
| `-u` | — | Target URL |
| `-i` | — | File of target URLs, one per line |
| `-w` | `default` | Wordlist file, or `default` for the built-in list |
| `-m` | `GET` | Request method: `GET`, `POST`, `JSON`, `XML` |
| `-t` | `15` | Concurrent workers |
| `-c` | auto | Parameters per request |
| `-rate-limit` | `20` | Starting and maximum requests per second |
| `-rate-min` | `1` | Floor the adaptive limiter will not drop below |
| `-rate-codes` | `429,503` | Status codes treated as rate limiting |
| `-no-adapt` | off | Hold the rate fixed instead of adapting |
| `-stable` | off | Single worker; trades speed for consistency |
| `-d` | `0` | Delay between requests, in seconds |
| `-T` | `15` | Request timeout, in seconds |
| `-retries` | `3` | Retries per request on transient network errors |
| `-headers` | — | Extra headers, separated by newlines or `\n` |
| `-include` | — | Parameters sent on every request, e.g. `a=b&c=d` |
| `-casing` | — | Rewrite candidates to `like_this`, `likeThis` or `likethis` |
| `-disable-redirects` | off | Do not follow redirects |
| `-state` | — | Checkpoint file that makes a scan resumable |
| `-resume` | off | Continue from the checkpoint file |
| `-o` / `-oJ` | — | Write results as JSON |
| `-oT` | — | Write results as text |
| `-oB` | — | Route requests through a proxy, e.g. Burp |
| `-q` | off | Quiet; suppress status output |
| `-no-color` | off | Disable coloured output |
| `-version` | — | Print version and exit |

## Good to know

- **A hit means "the server reacted", not "you found a bug."** Some parameters
  change a response for entirely boring reasons. Confirm by hand before it goes
  in a report.
- **Try `-casing`.** Applications are inconsistent about naming, and `user_id`,
  `userId` and `userid` are three different guesses. Running a pass in each
  style finds parameters a single pass misses.
- **Endpoints that respond randomly** — live counters, timestamps, rotating
  banners — produce noise, because paramvoid works by spotting differences.
  `-stable` and a slower rate help.
- **Scans behind authentication need `-headers`.** Without a session you are
  testing the logged-out surface, which is usually the dull half.

## Authorised use

Run it against systems you own or have written permission to test. Parameter
discovery is noisy and shows up plainly in logs.

## Credit

The approach — establish a baseline, then binary-search a wordlist for the
parameters a server reacts to — comes from
[Arjun](https://github.com/s0md3v/Arjun) by [s0md3v](https://github.com/s0md3v).
paramvoid is a separate implementation of that idea, not a fork.

## License

MIT — see [LICENSE](LICENSE).
