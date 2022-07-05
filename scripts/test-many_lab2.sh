#!/bin/bash

# This script helps you run multiple rounds of tests
# Example:
#     $ bash test-many.sh 2C 100 -race
# will run test 2C 100 times with race detector

# !WARN: You should NOT use the -race because the test configuration has data race with 
# the raft logger when clean up the test environment

if ! pwd | grep -q src/raft; then
    echo NOT under the correct path!
    echo Correct path is 6.824/src/raft
    exit
fi

if (($# < 2))
then
    echo Usage:
    echo   bash test-many.sh [test name] [test rounds]
    echo Example:
    echo   bash test-many.sh 2A 200
    exit
fi

begin=$((SECONDS))
for (( i=1; i<=$2; i++ ))
do
  if [ "$#" -eq 3 ]; then
    # run with `-race` option
    if ! go test -run $1 $3 -timeout 15m; 
    then
      echo Some tests fail! && exit
    fi
  else
    if ! go test -run $1 -timeout 15m; 
    then
      echo Some tests fail! && exit
    fi
  fi
  echo ============ Round $i Finished ==================
  rm -rf *.log
done
echo
echo "#########################################################"
echo "**** The $2 rounds of $1 tests took $((SECONDS-begin)) seconds"
echo "#########################################################"
echo 
echo "* Test runs on commit $(git log --pretty=format:'%H' -n 1)"
echo "* Environment Info:"
uname -a
echo -----------------------
lshw -short | head -10

