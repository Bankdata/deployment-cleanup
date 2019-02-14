#!/bin/sh

releasename="$1";
releasename=$(tr [A-Z] [a-z] <<< "$releasename")
releasename=$(cut -c 1-53 <<< "$releasename")

echo $releasename
