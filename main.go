package main

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

const (
	ExitCodeSuccess                   = 0
	ExitCodeErrorOpeningConfig        = 1
	ExitCodeErrorDeserializingConfig  = 2
	ExitCodeErrorLoginRequestBuilder  = 3
	ExitCodeErrorLoginRequest         = 4
	ExitCodeErrorWrongCredentials     = 5
	ExitCodeErrorExportRequestBuilder = 6
	ExitCodeErrorMissingCookie        = 7
	ExitCodeErrorExportRequest        = 8
)

type Config struct {
	ZabbixUsername string `yaml:"zabbix_username"`
	ZabbixPassword string `yaml:"zabbix_password"`
	ZabbixUrl      string `yaml:"zabbix_url"`
}

const config_filename = "config.yaml"

// loads and returns the content of config.yaml, on error it will exit
// with the proper error code
func load_config() Config {
	fmt.Printf("loading %s\n", config_filename)

	config_content, err := os.ReadFile(config_filename)

	if err != nil {
		fmt.Printf("failed to open %s, error: %s\n", config_filename, err.Error())
		os.Exit(ExitCodeErrorOpeningConfig)
	}

	var config Config
	err = yaml.Unmarshal([]byte(config_content), &config)

	if err != nil {
		fmt.Printf("failed to deserialize the config %s, error: %s\n", config_filename, err.Error())
		os.Exit(ExitCodeErrorDeserializingConfig)
	}

	return config
}

// returns a session cookie on successful login, otherwise it will
// exits the program with the proper error code
func zabbix_login(zabbix_url string, uname string, pass string) *http.Cookie {
	// encode the username, and password in case they contain unallowed characters
	uname = url.QueryEscape(uname)
	pass = url.QueryEscape(pass)

	form := url.Values{}
	form.Set("name", uname)
	form.Set("password", pass)
	form.Set("enter", "Sign in")
	form_body := form.Encode()

	request, err := http.NewRequest(http.MethodPost, zabbix_url+"/index.php", strings.NewReader(form_body))

	if err != nil {
		fmt.Printf("failed to build the login request, error: %s\n", err.Error())
		os.Exit(ExitCodeErrorLoginRequestBuilder)
	}

	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := http.Client{}

	// prevent a redirect, golang doesn't store the cookie so during natural redirect we cause the server to respond
	// with a cookie which with it we cannot access the zabbix as a user
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	response, err := client.Do(request)

	if err != nil {
		fmt.Printf("failed to login, error: %s\n", err.Error())
		os.Exit(ExitCodeErrorLoginRequest)
	}

	defer response.Body.Close()

	if response.StatusCode != http.StatusFound {
		fmt.Printf("wrong credentials, server responded didn't return the session cookie\n")
		os.Exit(ExitCodeErrorWrongCredentials)
		return nil
	}

	for _, cookie := range response.Cookies() {
		if cookie.Name == "zbx_session" {
			return cookie
		}
	}

	fmt.Printf("server didn't return a session cookie\n")
	os.Exit(ExitCodeErrorMissingCookie)
	return nil
}

// it will click the button export CSV and return the content of the file, on
// error it will exit with the proper error code
func zabbix_export_csv(zabbix_url string, session_cookie *http.Cookie) string {
	request, err := http.NewRequest(http.MethodGet, zabbix_url+"/zabbix.php?action=problem.view.csv", bytes.NewBuffer([]byte{}))

	if err != nil {
		fmt.Printf("failed to build the export CSV request, error: %s\n", err.Error())
		os.Exit(ExitCodeErrorExportRequestBuilder)
	}

	request.AddCookie(session_cookie)

	client := http.Client{}
	response, err := client.Do(request)

	if err != nil {
		fmt.Printf("failed to export the CSV, error: %s\n", err.Error())
		os.Exit(ExitCodeErrorExportRequest)
	}

	body, _ := io.ReadAll(response.Body)

	return string(body)
}

// extract only the active problems from the CSV file
func extract_active_problems(csv_content string) string {
	result := bytes.NewBufferString("")
	csv_reader := csv.NewReader(strings.NewReader(csv_content))

	fmt.Fprintf(result, "<table><tr><td style=\"text-align: center;\">Host</td><td style=\"text-align: center;\">Problem</td><td style=\"text-align: center;\">Time</td><td style=\"text-align: center;\">Duratiom</td></tr>\n")

	for {
		record, err := csv_reader.Read()

		if err != nil {
			break
		}

		status := record[3]
		host := record[4]
		problem := record[5]
		time := record[1]
		duration := record[6]

		if status != "PROBLEM" {
			continue
		}

		fmt.Fprintf(result, "<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n", host, problem, time, duration)
	}

	fmt.Fprintf(result, "</table>")

	return result.String()
}

// extract only the resolved problems from the CSV file
func extract_resolved_problems(csv_content string) string {
	result := bytes.NewBufferString("")
	csv_reader := csv.NewReader(strings.NewReader(csv_content))

	fmt.Fprintf(result, "<table><tr><td style=\"text-align: center;\">Host</td><td style=\"text-align: center;\">Problem</td><td style=\"text-align: center;\">Time</td><td style=\"text-align: center;\">Duratiom</td></tr>\n")

	for {
		record, err := csv_reader.Read()

		if err != nil {
			break
		}

		status := record[3]
		host := record[4]
		problem := record[5]
		time := record[1]
		duration := record[6]

		if status != "RESOLVED" {
			continue
		}

		fmt.Fprintf(result, "<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n", host, problem, time, duration)
	}

	fmt.Fprintf(result, "</table>")

	return result.String()
}

func main() {
	config := load_config()

	fmt.Printf("zabbix_username: %s\n", config.ZabbixUsername)
	fmt.Printf("zabbix_password: %s\n", strings.Repeat("*", len(config.ZabbixPassword)))
	fmt.Printf("zabbix_url: %s\n", config.ZabbixUrl)

	session_cookie := zabbix_login(config.ZabbixUrl, config.ZabbixUsername, config.ZabbixPassword)
	raw_csv := zabbix_export_csv(config.ZabbixUrl, session_cookie)

	html_problem_table := extract_active_problems(raw_csv)
	html_resolved_table := extract_resolved_problems(raw_csv)

	email_html := bytes.NewBufferString("")

	fmt.Fprintf(email_html, "%s", `<style>
    table {
        border: 1px solid;
        border-color: black;
        border-collapse: collapse;
    }

    tr {
        border: 1px solid;
        border-color: black;
        border-collapse: collapse;
    }

    td {
        border: 1px solid;
        border-color: black;
        border-collapse: collapse;
    }
</style>`)

	fmt.Fprintf(email_html, "<p style=\"text-align: center\">Active Problems<p>%s<p style=\"text-align: center\">Resolved Problems</p>%s\n", html_problem_table, html_resolved_table)

	time_now := time.Now().Local()

	// if it doesn't exist it will be created, otherwise it will fail and we can ignore the error
	os.Mkdir("report", 0755)
	os.WriteFile(fmt.Sprintf("report/report-%d-%d-%d-%d-%d-%d.html", time_now.Year(), time_now.Month(), time_now.Day(), time_now.Hour(), time_now.Minute(), time_now.Second()), []byte(email_html.String()), 0644)
}
