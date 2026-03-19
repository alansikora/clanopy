package auth

import "fmt"

const clanopyAppInstallURL = "https://github.com/apps/clanopy-review/installations/new"

// InstallClanopyReviewApp opens the browser to install the Clanopy Review App on a repo.
func InstallClanopyReviewApp(repo string) error {
	fmt.Printf("Opening browser to install the Clanopy Review app...\n")
	fmt.Printf("  → Select the repository: %s\n\n", repo)

	if err := openBrowser(clanopyAppInstallURL); err != nil {
		fmt.Printf("Open this URL in your browser:\n%s\n\n", clanopyAppInstallURL)
	}

	fmt.Printf("Press Enter after installing the app...")
	fmt.Scanln()
	fmt.Println()
	return nil
}
