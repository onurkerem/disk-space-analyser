# Disk Space Analyser CLI Tool (WIP)

This tool is for analysing disk space usage. It analyses all directories recursively in the computer. It will provide a report of the disk space usage. Report will include all directories and their sizes. Also it will provide a summary to show the largest directories and their sizes. 

## Prerequisites
- git
- MacOS or Linux

## Installation
```bash
git clone https://github.com/onurkerem/disk-space-analyser.git
cd disk-space-analyser
chmod +x disk-space-analyser.sh
```

## Usage

```bash
disk-space-analyser start # start the analyser in the background. terminal can be closed after starting.
disk-space-analyser stop # stop the analyser in the background.
disk-space-analyser status # get the status of the last analyser run.
```
## Output

```
See the report at http://localhost:3097/report
```

