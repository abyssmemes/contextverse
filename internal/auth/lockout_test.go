package auth

import (
	"errors"
	"testing"
)

// Counting failures against the username alone means anybody who knows a name
// can lock its owner out by typing the wrong password five times. That is a
// denial of service delivered for free, indistinguishable at the support desk
// from a real attack, and worse than the attack the lockout was for.
//
// Failures now count against the address as well, and the address budget is the
// tight one.

func passwordStore(t *testing.T, users ...string) *Store {
	t.Helper()
	s := newStore(t)
	for _, u := range users {
		if err := s.AddUser(u, RoleContributor); err != nil {
			t.Fatal(err)
		}
		if err := s.SetPassword(u, "correct-horse-battery"); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// The attack this must not permit: somebody else's failures locking you out.
func TestAnAttackerCannotLockSomebodyElseOut(t *testing.T) {
	s := passwordStore(t, "kim")

	// An attacker at one address burns their own budget against kim's account.
	for i := 0; i < MaxLoginFailures*2; i++ {
		_, _, _ = s.LoginUserpassFrom("kim", "wrong", "203.0.113.9")
	}

	// The attacker is stopped.
	if _, _, err := s.LoginUserpassFrom("kim", "correct-horse-battery", "203.0.113.9"); !errors.Is(err, ErrLockedOut) {
		t.Error("the attacker's address was not locked out")
	}

	// Kim, at her own address, can still log in. This is the whole point.
	if _, _, err := s.LoginUserpassFrom("kim", "correct-horse-battery", "198.51.100.4"); err != nil {
		t.Fatalf("the real owner was locked out by somebody else's failures: %v", err)
	}
}

// The address budget is what an attacker actually spends, so it has to bite
// quickly.
func TestOneAddressIsStoppedAfterItsBudget(t *testing.T) {
	s := passwordStore(t, "kim")
	const addr = "203.0.113.9"

	for i := 0; i < MaxLoginFailures; i++ {
		if _, _, err := s.LoginUserpassFrom("kim", "wrong", addr); errors.Is(err, ErrLockedOut) {
			t.Fatalf("locked out after only %d attempts, want %d", i, MaxLoginFailures)
		}
	}
	if _, _, err := s.LoginUserpassFrom("kim", "wrong", addr); !errors.Is(err, ErrLockedOut) {
		t.Errorf("still accepting attempts past the budget: %v", err)
	}
}

// An attacker working through a list of accounts from one machine is stopped by
// the address budget, not by locking every account they touch.
func TestGuessingManyAccountsFromOneAddressLocksTheAddress(t *testing.T) {
	s := passwordStore(t, "kim", "sam", "alex")
	const addr = "203.0.113.9"

	for _, u := range []string{"kim", "sam", "alex", "kim", "sam", "alex"} {
		_, _, _ = s.LoginUserpassFrom(u, "wrong", addr)
	}

	if _, _, err := s.LoginUserpassFrom("kim", "correct-horse-battery", addr); !errors.Is(err, ErrLockedOut) {
		t.Error("the address spraying accounts was not locked")
	}
	// None of the three accounts should be locked: each took two failures.
	for _, u := range []string{"kim", "sam", "alex"} {
		if s.LoginLocked(u) {
			t.Errorf("%s was locked out by an attack that never reached the account budget", u)
		}
	}
}

// An account still locks when failures arrive from many addresses, which is the
// case the account budget exists for.
func TestAnAccountLocksWhenFailuresComeFromEverywhere(t *testing.T) {
	s := passwordStore(t, "kim")

	for i := 0; i < MaxAccountFailures; i++ {
		// A different address every time, so no address budget is ever reached.
		addr := "203.0.113." + string(rune('a'+i%26)) + itoa(i)
		_, _, _ = s.LoginUserpassFrom("kim", "wrong", addr)
	}
	if !s.LoginLocked("kim") {
		t.Error("an account attacked from many addresses was never locked")
	}
}

// A correct password clears the account, but must not clear the address: one
// right answer cannot wipe the record of somebody working through a list from
// the same machine.
func TestASuccessDoesNotForgiveTheAddress(t *testing.T) {
	s := passwordStore(t, "kim", "sam")
	const addr = "203.0.113.9"

	for i := 0; i < MaxLoginFailures-1; i++ {
		_, _, _ = s.LoginUserpassFrom("sam", "wrong", addr)
	}
	// A legitimate-looking success from the same machine.
	if _, _, err := s.LoginUserpassFrom("kim", "correct-horse-battery", addr); err != nil {
		t.Fatal(err)
	}
	// One more failure should still reach the address budget.
	_, _, _ = s.LoginUserpassFrom("sam", "wrong", addr)
	if _, _, err := s.LoginUserpassFrom("sam", "wrong", addr); !errors.Is(err, ErrLockedOut) {
		t.Error("a success reset the attacker's address budget")
	}
}

// A username that looks like an address must not share its counter.
func TestUsernamesAndAddressesDoNotCollide(t *testing.T) {
	s := passwordStore(t, "203.0.113.9")

	// Burn the address budget from a different machine.
	for i := 0; i < MaxLoginFailures+1; i++ {
		_, _, _ = s.LoginUserpassFrom("somebody", "wrong", "198.51.100.4")
	}
	// The user named like an address is untouched.
	if s.LoginLocked("203.0.113.9") {
		t.Error("a username collided with an address counter")
	}
}

// A caller with no address to offer still works, counting against the account
// alone — a local console, or a test.
func TestLoginWithoutAnAddressStillWorks(t *testing.T) {
	s := passwordStore(t, "kim")
	if _, _, err := s.LoginUserpass("kim", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.LoginUserpass("kim", "wrong"); err == nil {
		t.Error("a wrong password was accepted")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
