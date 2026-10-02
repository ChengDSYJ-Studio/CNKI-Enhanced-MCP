import AppKit
import Foundation

func report(_ app: NSRunningApplication?, _ event: String) {
    let value: [String: Any] = ["event": event, "bundle": app?.bundleIdentifier ?? "unknown", "pid": app?.processIdentifier ?? 0]
    if let data = try? JSONSerialization.data(withJSONObject: value, options: [.sortedKeys]), let text = String(data: data, encoding: .utf8) {
        print(text)
        fflush(stdout)
    }
}
report(NSWorkspace.shared.frontmostApplication, "initial")
let token = NSWorkspace.shared.notificationCenter.addObserver(forName: NSWorkspace.didActivateApplicationNotification, object: nil, queue: .main) { notification in
    report(notification.userInfo?[NSWorkspace.applicationUserInfoKey] as? NSRunningApplication, "activated")
}
RunLoop.main.run()
