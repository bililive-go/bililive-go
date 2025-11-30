from playwright.sync_api import sync_playwright

def run(playwright):
    browser = playwright.chromium.launch(headless=True)
    page = browser.new_page()

    # Mock API response for notifications
    page.route("**/api/notifications", lambda route: route.fulfill(
        status=200,
        body='[{"id": 1, "type": "error", "message": "Test Notification Message", "status": "pending", "created_at": "2023-01-01T00:00:00Z"}]',
        headers={"Content-Type": "application/json"}
    ))

    # Visit the served app
    page.goto("http://localhost:8081")

    # Wait for the notification to appear
    page.wait_for_selector(".ant-alert")

    # Take screenshot
    page.screenshot(path="verification/notification.png")

    browser.close()

with sync_playwright() as playwright:
    run(playwright)
