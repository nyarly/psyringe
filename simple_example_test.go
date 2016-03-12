package syringe_test

import (
	"fmt"
	"log"

	"github.com/samsalisbury/syringe"
)

type (
	Command struct {
		User Username
		Host Hostname
		Load LoadAverage
	}
	Username    string
	Hostname    string
	LoadAverage float64
)

func NewUsername() Username       { return "bob" }
func NewHostname() Hostname       { return Hostname("localhost") }
func NewLoadAverage() LoadAverage { return 0.83 }

func (c Command) Print() {
	fmt.Printf("User: %s, Host: %s, Load average: %.2f", c.User, c.Host, c.Load)
}

func ExampleSyringe_Simple() {
	s := syringe.Syringe{}
	if err := s.Fill(NewUsername, NewHostname, NewLoadAverage); err != nil {
		log.Fatal(err)
	}
	command := Command{}
	s.Inject(&command)
	command.Print()
	// output:
	// User: bob, Host: localhost, Load average: 0.83
}
