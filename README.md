# procmon
A process monitor &amp; control application.

# Features
- Auto restart died applications which been monitored.
- Remote client tool for running applications.
	- Upload/Rollback files.
	- Execute commands.
	- Client tool authentication.

# Install

    go get github.com/golang/glog
	go get github.com/yinqiwen/procmon/{pmond,pmonc}


# Start Server

    Usage of ./pmond:
     -alsologtostderr
        log to standard error as well as files
     -conf string
        config file (default "./conf/pmon.json")
     -log_backtrace_at value
        when logging hits line file:N, emit a stack trace (default :0)
     -log_dir string
        If non-empty, write log files in this directory
     -logtostderr
        log to standard error instead of files
     -stderrthreshold value
        logs at or above this threshold go to stderr
     -v value
        log level for V logs
     -vmodule value
        comma-separated list of pattern=N settings for file-filtered logging

	./pmond -conf ./conf/procmon.json

   The json config example:

	{
    "Listen": "0.0.0.0:60000",
    "Auth": "password",
    "Monitor": [
        {
            "Proc":"./example1 -log_dir log1"
        },
        {
            "Proc":"./example2 -log_dir log2"
        }
     ]
    }



# Client Usage
## Upload File
	pmonc -servers 1.1.1.1:60000,1.2.2.2:60000 -c 1 -upload bin/myapp
## Rollback Uploaded File
	pmonc -servers 1.1.1.1:60000,1.2.2.2:60000 -c 1 -cmd "rollback bin/myapp"
## Exec Command
	pmonc -servers 1.1.1.1:60000,1.2.2.2:60000 -c 1 -cmd "ls -l"

# LICENSE
