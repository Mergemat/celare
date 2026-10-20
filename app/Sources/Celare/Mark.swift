import AppKit
import SwiftUI

/// celare's mark: a thick ring open to the right, around a dot. Defined in
/// a 24-unit square, like the SVG on the site, and drawn from this one
/// definition everywhere: menu bar, the menu, the app icon.
enum Mark {
    static let unit: CGFloat = 24
    static let ringRadius: CGFloat = 8.5
    static let stroke: CGFloat = 3.2
    static let dotRadius: CGFloat = 2.6
    /// Half the opening, measured from the right.
    static let gap: CGFloat = 40

    static func ring(in rect: CGRect) -> Path {
        let s = rect.width / unit
        var p = Path()
        p.addArc(center: CGPoint(x: rect.midX, y: rect.midY), radius: ringRadius * s,
                 startAngle: .degrees(gap), endAngle: .degrees(360 - gap), clockwise: false)
        return p
    }

    static func dot(in rect: CGRect) -> Path {
        let r = dotRadius * rect.width / unit
        return Path(ellipseIn: CGRect(x: rect.midX - r, y: rect.midY - r, width: 2 * r, height: 2 * r))
    }

    /// The menu bar icon: the ring alone when disconnected, with its dot
    /// when connected. A template image, so macOS tints it for the bar.
    static func menuBarImage(connected: Bool) -> NSImage {
        let image = NSImage(size: NSSize(width: 18, height: 18), flipped: true) { rect in
            let s = rect.width / unit
            let center = NSPoint(x: rect.midX, y: rect.midY)
            let ring = NSBezierPath()
            ring.appendArc(withCenter: center, radius: ringRadius * s, startAngle: gap, endAngle: 360 - gap, clockwise: false)
            ring.lineWidth = stroke * s
            ring.lineCapStyle = .round
            NSColor.black.setStroke()
            ring.stroke()
            if connected {
                let r = dotRadius * s
                NSColor.black.setFill()
                NSBezierPath(ovalIn: NSRect(x: center.x - r, y: center.y - r, width: 2 * r, height: 2 * r)).fill()
            }
            return true
        }
        image.isTemplate = true
        return image
    }
}

/// The mark as a SwiftUI view, in the current foreground style.
struct MarkView: View {
    var dot = true

    var body: some View {
        Canvas { ctx, size in
            let side = min(size.width, size.height)
            let rect = CGRect(x: (size.width - side) / 2, y: (size.height - side) / 2, width: side, height: side)
            ctx.stroke(Mark.ring(in: rect), with: .foreground,
                       style: StrokeStyle(lineWidth: Mark.stroke * side / Mark.unit, lineCap: .round))
            if dot { ctx.fill(Mark.dot(in: rect), with: .foreground) }
        }
    }
}

/// The app icon: the mark in white on the app's convex deep blue, on the
/// macOS icon grid (an 824-point tile in a 1024-point canvas).
struct AppIconView: View {
    var body: some View {
        let tile = RoundedRectangle(cornerRadius: 185, style: .continuous)
        ZStack {
            tile
                .fill(LinearGradient(colors: [Color(red: 0.29, green: 0.46, blue: 0.88), Color(red: 0.07, green: 0.18, blue: 0.48)],
                                     startPoint: .top, endPoint: .bottom))
                .overlay(
                    tile.inset(by: 3).stroke(
                        LinearGradient(colors: [.white.opacity(0.45), .clear], startPoint: .top, endPoint: .center),
                        lineWidth: 6)
                )
                .overlay(tile.stroke(Color.black.opacity(0.35), lineWidth: 2))
                .shadow(color: .black.opacity(0.32), radius: 18, y: 12)
            MarkView()
                .foregroundStyle(.white)
                .frame(width: 520, height: 520)
                .shadow(color: .black.opacity(0.28), radius: 6, y: 5)
        }
        .frame(width: 824, height: 824)
        .frame(width: 1024, height: 1024)
    }

    /// Renders the 1024-pixel master PNG `make icon` turns into AppIcon.icns.
    @MainActor
    static func write(to path: String) -> Bool {
        let renderer = ImageRenderer(content: AppIconView())
        renderer.scale = 1
        guard let image = renderer.nsImage, let tiff = image.tiffRepresentation,
              let png = NSBitmapImageRep(data: tiff)?.representation(using: .png, properties: [:]) else { return false }
        return (try? png.write(to: URL(fileURLWithPath: path))) != nil
    }
}
