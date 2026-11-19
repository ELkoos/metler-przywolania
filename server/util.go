package main

import "io"

func multiWriter(w ...io.Writer) io.Writer { return io.MultiWriter(w...) }
