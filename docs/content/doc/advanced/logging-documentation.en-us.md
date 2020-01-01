---
date: "2019-04-02T17:06:00+01:00"
title: "Advanced: Logging Configuration"
slug: "logging-configuration"
weight: 55
toc: true
draft: false
menu:
  sidebar:
    parent: "advanced"
    name: "Logging Configuration"
    weight: 55
    identifier: "logging-configuration"
---

# Logging Configuration

The logging framework has been revamped in Gitea 1.9.0.

## Log Groups

The fundamental thing to be aware of in Gitea is that there are several
log groups:

* The "Default" logger
* The Macaron logger
* The Router logger
* The Access logger
* The XORM logger

There is also the go log logger.

### The go log logger

Go provides its own extremely basic logger in the `log` package,
however, this is not sufficient for our purposes as it does not provide
a way of logging at multiple levels, nor does it provide a good way of
controlling where these logs are logged except through setting of a
writer.

We have therefore redirected this logger to our Default logger, and we
will log anything that is logged using the go logger at the INFO level.

### The "Default" logger

Calls to `log.Info`, `log.Debug`, `log.Error` etc. from the `code.gitea.io/gitea/modules/log` package will log to this logger.

You can configure the outputs of this logger by setting the `MODE`
value in the `[log]` section of the configuration.

Each output sublogger is configured in a separate `[log.sublogger]`
section, but there are certain default values. These will not be inherited from the `[log]` section:

* `FLAGS` is `stdflags` (Equal to
`date,time,medfile,shortfuncname,levelinitial`)
* `FILE_NAME` will default to `%(ROOT_PATH)/gitea.log`
* `EXPRESSION` will default to `""`
* `PREFIX` will default to `""`

The provider type of the sublogger can be set using the `MODE` value in
its subsection, but will default to the name. This allows you to have
multiple subloggers that will log to files.

### The "Macaron" logger

By default Macaron will log to its own go `log` instance. This writes
to `os.Stdout`. You can redirect this log to a Gitea configurable logger
through setting the `REDIRECT_MACARON_LOG` setting in the `[log]`
section which you can configure the outputs of by setting the `MACARON`
value in the `[log]` section of the configuration. `MACARON` defaults
to `file` if unset.

Each output sublogger for this logger is configured in
`[log.sublogger.macaron]` sections. There are certain default values
which will not be inherited from the `[log]` or relevant
`[log.sublogger]` sections:

* `FLAGS` is `stdflags` (Equal to
`date,time,medfile,shortfuncname,levelinitial`)
* `FILE_NAME` will default to `%(ROOT_PATH)/macaron.log`
* `EXPRESSION` will default to `""`
* `PREFIX` will default to `""`

NB: You can redirect the macaron logger to send its events to the gitea
log using the value: `MACARON = ,`

### The "Router" logger

There are two types of Router log. By default Macaron send its own
router log which will be directed to Macaron's go `log`, however if you
`REDIRECT_MACARON_LOG` you will enable Gitea's router log. You can
disable both types of Router log by setting `DISABLE_ROUTER_LOG`.

If you enable the redirect, you can configure the outputs of this
router log by setting the `ROUTER` value in the `[log]` section of the
configuration. `ROUTER` will default to `console` if unset. The Gitea
Router logs the same data as the Macaron log but has slightly different
coloring. It logs at the `Info` level by default, but this can be
changed if desired by setting the `ROUTER_LOG_LEVEL` value.

Each output sublogger for this logger is configured in
`[log.sublogger.router]` sections. There are certain default values
which will not be inherited from the `[log]` or relevant
`[log.sublogger]` sections:

* `FILE_NAME` will default to `%(ROOT_PATH)/router.log`
* `FLAGS` defaults to `date,time`
* `EXPRESSION` will default to `""`
* `PREFIX` will default to `""`

NB: You can redirect the router logger to send its events to the Gitea
log using the value: `ROUTER = ,`

### The "Access" logger

The Access logger is a new logger for version 1.9. It provides a NCSA
Common Log compliant log format. It's highly configurable but caution
should be taken when changing its template. The main benefit of this
logger is that Gitea can now log accesses in a standard log format so
standard tools may be used.

You can enable this logger using `ENABLE_ACCESS_LOG`. Its outputs are
configured by setting the `ACCESS` value in the `[log]` section of the
configuration. `ACCESS` defaults to `file` if unset.

Each output sublogger for this logger is configured in
`[log.sublogger.access]` sections. There are certain default values
which will not be inherited from the `[log]` or relevant
`[log.sublogger]` sections:

* `FILE_NAME` will default to `%(ROOT_PATH)/access.log`
* `FLAGS` defaults to `` or None
* `EXPRESSION` will default to `""`
* `PREFIX` will default to `""`

If desired the format of the Access logger can be changed by changing
the value of the `ACCESS_LOG_TEMPLATE`.

NB: You can redirect the access logger to send its events to the Gitea
log using the value: `ACCESS = ,`

#### The ACCESS_LOG_TEMPLATE

This value represent a go template. It's default value is:

`{{.Ctx.RemoteAddr}} - {{.Identity}} {{.Start.Format "[02/Jan/2006:15:04:05 -0700]" }} "{{.Ctx.Req.Method}} {{.Ctx.Req.URL.RequestURI}} {{.Ctx.Req.Proto}}" {{.ResponseWriter.Status}} {{.ResponseWriter.Size}} "{{.Ctx.Req.Referer}}\" \"{{.Ctx.Req.UserAgent}}"`

The template is passed following options:

* `Ctx` is the `macaron.Context`
* `Identity` is the `SignedUserName` or `"-"` if the user is not logged
in
* `Start` is the start time of the request
* `ResponseWriter` is the `macaron.ResponseWriter`

Caution must be taken when changing this template as it runs outside of
the standard panic recovery trap. The template should also be as simple
as it runs for every request.

### The "XORM" logger

The XORM logger is a long-standing logger that exists to collect XORM
log events. It is enabled by default but can be switched off by setting
`ENABLE_XORM_LOG` to `false` in the `[log]` section. Its outputs are
configured by setting the `XORM` value in the `[log]` section of the
configuration. `XORM` defaults to `,` if unset, meaning it is redirected
to the main Gitea log.

XORM will log SQL events by default. This can be changed by setting
the `LOG_SQL` value to `false` in the `[database]` section.

Each output sublogger for this logger is configured in
`[log.sublogger.xorm]` sections. There are certain default values
which will not be inherited from the `[log]` or relevant
`[log.sublogger]` sections:

* `FILE_NAME` will default to `%(ROOT_PATH)/xorm.log`
* `FLAGS` defaults to `date,time`
* `EXPRESSION` will default to `""`
* `PREFIX` will default to `""`

## Log outputs

Gitea provides 4 possible log outputs:

* `console` - Log to `os.Stdout` or `os.Stderr`
* `file` - Log to a file
* `conn` - Log to a keep-alive TCP connection
* `smtp` - Log via email

Certain configuration is common to all modes of log output:

* `LEVEL` is the lowest level that this output will log. This value
is inherited from `[log]` and in the case of the non-default loggers
from `[log.sublogger]`.
* `STACKTRACE_LEVEL` is the lowest level that this output will print
a stacktrace. This value is inherited.
* `MODE` is the mode of the log output. It will default to the sublogger
name. Thus `[log.console.macaron]` will default to `MODE = console`.
* `COLORIZE` will default to `true` for `console` as
described, otherwise it will default to `false`.

### Non-inherited default values

There are several values which are not inherited as described above but
rather default to those specific to type of logger, these are:
`EXPRESSION`, `FLAGS`, `PREFIX` and `FILE_NAME`.

#### `EXPRESSION`

`EXPRESSION` represents a regular expression that log events must match to be logged by the sublogger. Either the log message, (with colors removed), must match or the `longfilename:linenumber:functionname` must match. NB: the whole message or string doesn't need to completely match.

Please note this expression will be run in the sublogger's goroutine
not the logging event subroutine. Therefore it can be complicated.

#### `FLAGS`

`FLAGS` represents the preceding logging context information that is
printed before each message. It is a comma-separated string set. The order of values does not matter.

Possible values are:

* `none` or `,` - No flags.
* `date` - the date in the local time zone: `2009/01/23`.
* `time` - the time in the local time zone: `01:23:23`.
* `microseconds` - microsecond resolution: `01:23:23.123123`. Assumes
time.
* `longfile` - full file name and line number: `/a/b/c/d.go:23`.
* `shortfile` - final file name element and line number: `d.go:23`.
* `funcname` - function name of the caller: `runtime.Caller()`.
* `shortfuncname` - last part of the function name. Overrides
`funcname`.
* `utc` - if date or time is set, use UTC rather than the local time
zone.
* `levelinitial` - Initial character of the provided level in brackets eg. `[I]` for info.
* `level` - Provided level in brackets `[INFO]`
* `medfile` - Last 20 characters of the filename - equivalent to
`shortfile,longfile`.
* `stdflags` - Equivalent to `date,time,medfile,shortfuncname,levelinitial`

### Console mode

For loggers in console mode, `COLORIZE` will default to `true` if not
on windows, or the windows terminal can be set into ANSI mode or is a
cygwin or Msys pipe.

If `STDERR` is set to `true` the logger will use `os.Stderr` instead of
`os.Stdout`.

### File mode

The `FILE_NAME` defaults as described above. If set it will be relative
to the provided `ROOT_PATH` in the master `[log]` section.

Other values:

* `LOG_ROTATE`: **true**: Rotate the log files.
* `MAX_SIZE_SHIFT`: **28**: Maximum size shift of a single file, 28 represents 256Mb.
* `DAILY_ROTATE`: **true**: Rotate logs daily.
* `MAX_DAYS`: **7**: Delete the log file after n days
* `COMPRESS`: **true**: Compress old log files by default with gzip
* `COMPRESSION_LEVEL`: **-1**: Compression level

### Conn mode

* `RECONNECT_ON_MSG`: **false**: Reconnect host for every single message.
* `RECONNECT`: **false**: Try to reconnect when connection is lost.
* `PROTOCOL`: **tcp**: Set the protocol, either "tcp", "unix" or "udp".
* `ADDR`: **:7020**: Sets the address to connect to.

### SMTP mode

It is not recommended to use this logger to send general logging
messages. However, you could perhaps set this logger to work on `FATAL`.

* `USER`: User email address to send from.
* `PASSWD`: Password for the smtp server.
* `HOST`: **127.0.0.1:25**: The SMTP host to connect to.
* `RECEIVERS`: Email addresses to send to.
* `SUBJECT`: **Diagnostic message from Gitea**

## Default Configuration

The default empty configuration is equivalent to:

```ini
[log]
ROOT_PATH = %(GITEA_WORK_DIR)/log
MODE = console
LEVEL = Info
STACKTRACE_LEVEL = None
REDIRECT_MACARON_LOG = false
ENABLE_ACCESS_LOG = false
ENABLE_XORM_LOG = true
XORM = ,

[log.console]
MODE = console
LEVEL = %(LEVEL)
STACKTRACE_LEVEL = %(STACKTRACE_LEVEL)
FLAGS = stdflags
PREFIX =
COLORIZE = true # Or false if your windows terminal cannot color
```

This is equivalent to sending all logs to the console, with default go log being sent to the console log too.

## Log colorization

Logs to the console will be colorized by default when not running on
Windows. Terminal sniffing will occur on Windows and if it is
determined that we are running on a terminal capable of color we will
colorize.

Further, on *nix it is becoming common to have file logs that are
colored by default. Therefore file logs will be colorised by default
when not running on Windows.

You can switch on or off colorization by using the `COLORIZE` value.

From a development point of view. If you write
`log.Info("A %s string", "formatted")` the `formatted` part of the log
message will be Bolded on colorized logs.

You can change this by either rendering the formatted string yourself.
Or you can wrap the value in a `log.ColoredValue` struct.

The `log.ColoredValue` struct contains a pointer to value, a pointer to
string of bytes which should represent a color and second set of reset
bytes. Pointers were chosen to prevent copying of large numbers of
values. There are several helper methods:

* `log.NewColoredValue` takes a value and 0 or more color attributes
that represent the color. If 0 are provided it will default to a cached
bold. Note, it is recommended that color bytes constructed from
attributes should be cached if this is a commonly used log message.
* `log.NewColoredValuePointer` takes a pointer to a value, and
0 or more color attributes that represent the color.
* `log.NewColoredValueBytes` takes a value and a pointer to an array
of bytes representing the color.

These functions will not double wrap a `log.ColoredValue`. They will
also set the `resetBytes` to the cached `resetBytes`.

The `colorBytes` and `resetBytes` are not exported to prevent
accidental overwriting of internal values.

## ColorFormat & ColorFormatted

Structs may implement the `log.ColorFormatted` interface by implementing the `ColorFormat(fmt.State)` function.

If a `log.ColorFormatted` struct is logged with `%-v` format, its `ColorFormat` will be used instead of the usual `%v`. The full `fmt.State` will be passed to allow implementers to look at additional flags.

In order to help implementers provide `ColorFormat` methods. There is a
`log.ColorFprintf(...)` function in the log module that will wrap values in `log.ColoredValue` and recognise `%-v`.

In general it is recommended not to make the results of this function too verbose to help increase its versatility. Usually this should simply be an `ID`:`Name`. If you wish to make a more verbose result, it is recommended to use `%-+v` as your marker.

## Log Spoofing protection

In order to protect the logs from being spoofed with cleverly
constructed messages. Newlines are now prefixed with a tab and control
characters except those used in an ANSI CSI are escaped with a
preceding `\` and their octal value.

## Creating a new named logger group

Should a developer wish to create a new named logger, `NEWONE`. It is
recommended to add an `ENABLE_NEWONE_LOG` value to the `[log]`
section, and to add a new `NEWONE` value for the modes.

A function like `func newNewOneLogService()` is recommended to manage
construction of the named logger. e.g.

```go
func newNewoneLogService() {
	EnableNewoneLog = Cfg.Section("log").Key("ENABLE_NEWONE_LOG").MustBool(false)
	Cfg.Section("log").Key("NEWONE").MustString("file") // or console? or "," if you want to send this to default logger by default
	if EnableNewoneLog {
		options := newDefaultLogOptions()
		options.filename = filepath.Join(LogRootPath, "newone.log")
		options.flags = "stdflags"
		options.bufferLength = Cfg.Section("log").Key("BUFFER_LEN").MustInt64(10000)
		generateNamedLogger("newone", options)
	}
}
```

You should then add `newOneLogService` to `NewServices()` in
`modules/setting/setting.go`
