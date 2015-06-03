# riemann-docker-agent

Monitor docker events and route them to Riemann

#### Build

    $ go build
    
#### Usage

    $ ./riemann-docker-agent --help

Events sent to Riemann can be deeply customized by command-line flags.
The following fields can be configured with a template on the event
received by docker:

 - service
 - description
 - state
 - tags
 - attributes

The syntax is the usual *golang templates* syntax, while the available
fields are:

 - Host: host provided by command line
 - ContainerId: id of the container
 - Status: event's status (such as *create*, *die*, *destroy*, refer to docker's docs)
 - Image: container image name
 - Name: container name if available or container id
 - ContainerInfo: container info struct as exported by *dockerclient*


ContainerInfo has the following structure:

    type ContainerInfo struct {
        Id              string
        Created         string
        Path            string
        Name            string
        Args            []string
        ExecIDs         []string
        Config          *ContainerConfig
        State           *State
        Image           string
        NetworkSettings struct {
            IPAddress   string `json:"IpAddress"`
            IPPrefixLen int    `json:"IpPrefixLen"`
            Gateway     string
            Bridge      string
            Ports       map[string][]PortBinding
        }
        SysInitPath    string
        ResolvConfPath string
        Volumes        map[string]string
        HostConfig     *HostConfig
    }
 
Please notice that ContainerInfo (and thus the container's Name) is by design
unavailable for some event types (such as *destroy*)

#### Example

    $ ./riemann-docker-agent -h my.host -t docker -t 'docker-{{.Status}}' -d 'Docker events for container {{.Name}} created on {{.ContainerInfo.Created}}' -a docker-host={{.Host}} -v
    2015/06/03 13:19:40 Connected to docker @ tcp://192.168.59.103:2375 (version 1.6.2)
    2015/06/03 13:19:42 Received event &{Id:bd9d6be76033648afedaaa18f5233cf4ab39ab707b50e15dbfbfaaac04e1924a Status:create From:ubuntu:trusty Time:1433330370}
    2015/06/03 13:19:42 connected to riemann on tcp://localhost:5555
    2015/06/03 13:19:42 Sending event {Ttl:0 Time:1433330370 Tags:[docker docker-create] Host:my.host State:create Service:docker test-container create Metric:0 Description:Docker events for container test-container created on 2015-06-03T11:19:30.919873198Z Attributes:map[docker-host:my.host]}
    2015/06/03 13:19:42 Received event &{Id:bd9d6be76033648afedaaa18f5233cf4ab39ab707b50e15dbfbfaaac04e1924a Status:start From:ubuntu:trusty Time:1433330371}
    2015/06/03 13:19:42 Sending event {Ttl:0 Time:1433330371 Tags:[docker docker-start] Host:my.host State:start Service:docker test-container start Metric:0 Description:Docker events for container test-container created on 2015-06-03T11:19:30.919873198Z Attributes:map[docker-host:my.host]}
    2015/06/03 13:19:42 Received event &{Id:bd9d6be76033648afedaaa18f5233cf4ab39ab707b50e15dbfbfaaac04e1924a Status:die From:ubuntu:trusty Time:1433330371}
    2015/06/03 13:19:42 Sending event {Ttl:0 Time:1433330371 Tags:[docker docker-die] Host:my.host State:die Service:docker test-container die Metric:0 Description:Docker events for container test-container created on 2015-06-03T11:19:30.919873198Z Attributes:map[docker-host:my.host]}
