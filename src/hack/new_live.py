#!/usr/bin/env python
import os.path
import string
import sys
from argparse import ArgumentParser

t = string.Template('''package ${package}

import (
    "net/url"
    
    "github.com/bililive-go/bililive-go/src/live"
    "github.com/bililive-go/bililive-go/src/live/internal"
)

const (
    domain = "${domain}"
    cnName = "${cn_name}"
)

func init() {
    live.Register(domain, new(builder))
}

type builder struct{}

func (b *builder) Build(url *url.URL) (live.Live, error) {
    return &Live{
        BaseLive: internal.NewBaseLive(url),
    }, nil
}

type Live struct {
    internal.BaseLive
}

func (l *Live) GetInfo() (info *live.Info, err error) {
    // TODO: Implement this method
    return nil, nil
}

func (l *Live) GetStreamUrls() (us []*url.URL, err error) {
    // TODO: Implement this method
    return nil, nil
}

func (l *Live) GetPlatformCNName() string {
    return cnName
}
''')


def main():
    parser = ArgumentParser()
    parser.add_argument('--package', required=True, dest='package', help='package name')
    parser.add_argument('--domain', required=True, dest='domain', help='domain of site, like: "live.bilibili.com"')
    parser.add_argument('--cn-name', required=True, dest='cn_name', help='site`s name in chinese')
    args = parser.parse_args()

    if os.path.exists('./src/live/{}'.format(args.package)):
        print('package: {} is exist'.format(args.package), sys.stderr)
        exit(1)
    code = t.substitute({
        'package': args.package,
        'domain': args.domain,
        'cn_name': args.cn_name
    })
    os.mkdir('./src/live/{}'.format(args.package))
    with open('./src/live/{}/{}.go'.format(args.package, args.package), 'w') as f:
        f.write(code)


if __name__ == '__main__':
    main()
