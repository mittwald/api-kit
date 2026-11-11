package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/auth"
	"github.com/mittwald/api-kit/definition"
)

// authCommand returns the commands around the session of the user: where
// the token of a run comes from, forgetting the stored session, and the
// logins the definition describes.
func (a *App) authCommand(doc *definition.Document) *cobra.Command {
	command := &cobra.Command{
		Use:   "auth",
		Short: "Show, remove or obtain the session used for requests",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := cmd.Help(); err != nil {
				return err
			}
			return errSilent{code: apikit.ExitFailed}
		},
	}
	command.AddCommand(
		&cobra.Command{
			Use:   "status",
			Short: "Show where the API token comes from and whether the API accepts it",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				session, err := a.session(doc)
				if err != nil {
					return err
				}
				source, _, err := session.auth.Status(cmd.Context())
				switch {
				case err == nil:
					_, err = fmt.Fprintln(cmd.OutOrStdout(), "Using API token from "+session.describe(source))
					return failed(err)
				case errors.Is(err, auth.ErrNoToken):
					// Not being logged in is an answer, not a failure.
					_, err = fmt.Fprintln(cmd.OutOrStdout(), "Not authenticated")
					return failed(err)
				}
				return failed(err)
			},
		},
		&cobra.Command{
			Use:   "reset",
			Short: "Remove the stored session",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				session, err := a.session(doc)
				if err != nil {
					return err
				}
				existed, err := session.auth.Logout()
				if err != nil {
					return failed(err)
				}
				message := "No session is stored for " + session.environment
				if existed {
					message = "Removed the stored session for " + session.environment
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), message)
				return failed(err)
			},
		},
	)

	login := func(use, short, method string) *cobra.Command {
		return &cobra.Command{
			Use:   use,
			Short: short,
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				session, err := a.session(doc)
				if err != nil {
					return err
				}
				if _, err := session.auth.Login(cmd.Context(), method); err != nil {
					return failed(err)
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "Stored the session for "+session.environment)
				return failed(err)
			},
		}
	}
	methods := doc.Auth.Login.Methods
	// "login" alone is the method an interactive run falls back to, or the
	// only one there is.
	switch {
	case doc.Auth.Login.Fallback != "":
		command.AddCommand(login("login", "Log in and store the session", doc.Auth.Login.Fallback))
	case len(methods) == 1:
		command.AddCommand(login("login", "Log in and store the session", methods[0].Name))
	}
	for _, method := range methods {
		short := "Log in with a password and store the session"
		if method.Kind == definition.LoginBrowser {
			short = "Log in through the browser and store the session"
		}
		command.AddCommand(login("login-"+method.Name, short, method.Name))
	}
	return command
}

// describe names the source of a token the way a user knows it.
func (s *session) describe(source auth.Source) string {
	switch source.Kind {
	case auth.SourceExplicit:
		return "--" + flagToken
	case auth.SourceStore:
		return "the stored session for " + s.environment
	case auth.SourceEnv:
		return "the environment variable " + source.Name
	case auth.SourceRefresh:
		return "the renewed session for " + s.environment
	case auth.SourceLogin:
		return "the login just made"
	}
	return "this run"
}

// hint returns what to tell the user after an error, "" when there is
// nothing to add. A missing token is helped by a login, an API that gives
// no verdict by patience.
func (a *App) hint(err error, hasLogin bool) string {
	switch {
	case errors.Is(err, auth.ErrInconclusive):
		return "Try again once the API is reachable."
	case errors.Is(err, auth.ErrNoToken) && hasLogin:
		return "Run '" + a.opts.Name + " auth login' to log in."
	}
	return ""
}
