#!/bin/bash
shopt -s extglob
rm -rf !(main).go unused
mkdir tmp
touch tmp/LICENSE
git fetch https://github.com/dominikh/go-tools master
git --work-tree=./tmp checkout FETCH_HEAD -- 'LICENSE' 'LICENSE-THIRD-PARTY' 'unused'
mkdir unused
mv tmp/unused/* unused
mv tmp/LICENSE* unused
go mod tidy
gofmt -w -r 'run -> Run' ./unused
gofmt -w -r 'id -> ID' ./unused
gofmt -w -r 'obj -> Obj' ./unused
gofmt -w -r 'uses -> Uses' ./unused
gofmt -w -r 'owns -> Owns' ./unused
gofmt -w -r 'Obj -> obj' ./unused/testdata
rmdir tmp/unused
rmdir tmp
git reset