import AppKit
import SwiftUI

/// Deep blue is the primary color. Everything interactive is convex: a
/// faint top-lit gradient, a hairline border and a tight shadow, pressing
/// in when held. Surfaces that only show information stay flat, so shape
/// alone tells you what can be clicked.
enum Palette {
    static let blueTop = Color(red: 0.22, green: 0.38, blue: 0.80)
    static let blueBottom = Color(red: 0.09, green: 0.21, blue: 0.56)
    static let blue = Color(red: 0.13, green: 0.29, blue: 0.70)

    static let neutralTop = adaptive(light: (1.00, 1.00, 1.00), dark: (0.27, 0.28, 0.31))
    static let neutralBottom = adaptive(light: (0.91, 0.92, 0.94), dark: (0.19, 0.20, 0.22))

    private static func adaptive(light: (CGFloat, CGFloat, CGFloat), dark: (CGFloat, CGFloat, CGFloat)) -> Color {
        Color(nsColor: NSColor(name: nil) { appearance in
            let c = appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua ? dark : light
            return NSColor(srgbRed: c.0, green: c.1, blue: c.2, alpha: 1)
        })
    }
}

struct Convex: ViewModifier {
    var prominent: Bool
    var pressed: Bool
    var radius: CGFloat

    func body(content: Content) -> some View {
        let top = prominent ? Palette.blueTop : Palette.neutralTop
        let bottom = prominent ? Palette.blueBottom : Palette.neutralBottom
        let shape = RoundedRectangle(cornerRadius: radius, style: .continuous)
        content
            .foregroundStyle(prominent ? Color.white : Color.primary)
            .background(shape.fill(LinearGradient(colors: pressed ? [bottom, top] : [top, bottom], startPoint: .top, endPoint: .bottom)))
            .overlay(
                shape.strokeBorder(
                    LinearGradient(colors: [.white.opacity(prominent ? 0.30 : 0.55), .black.opacity(prominent ? 0.25 : 0.14)],
                                   startPoint: .top, endPoint: .bottom),
                    lineWidth: 0.75
                )
            )
            .shadow(color: .black.opacity(pressed ? 0 : 0.14), radius: 1, y: pressed ? 0 : 0.75)
            .animation(.easeOut(duration: 0.08), value: pressed)
    }
}

struct ConvexButtonStyle: ButtonStyle {
    var prominent = false
    var fullWidth = false
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 12, weight: .medium))
            .padding(.horizontal, 10)
            .frame(height: 24)
            .frame(maxWidth: fullWidth ? .infinity : nil)
            .modifier(Convex(prominent: prominent, pressed: configuration.isPressed, radius: 6))
            .opacity(isEnabled ? 1 : 0.45)
            .contentShape(Rectangle())
    }
}

/// A small square button holding an SF Symbol.
struct IconButtonStyle: ButtonStyle {
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 11, weight: .semibold))
            .frame(width: 26, height: 22)
            .modifier(Convex(prominent: false, pressed: configuration.isPressed, radius: 6))
            .opacity(isEnabled ? 1 : 0.45)
            .contentShape(Rectangle())
    }
}

/// Rows in the node list: full width, blue when active.
struct ConvexRowStyle: ButtonStyle {
    var active: Bool

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .padding(.horizontal, 9)
            .padding(.vertical, 6)
            .frame(maxWidth: .infinity, alignment: .leading)
            .modifier(Convex(prominent: active, pressed: configuration.isPressed, radius: 7))
            .contentShape(Rectangle())
    }
}

struct PowerButtonStyle: ButtonStyle {
    var on: Bool
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 14, weight: .semibold))
            .frame(width: 34, height: 34)
            .modifier(Convex(prominent: on, pressed: configuration.isPressed, radius: 17))
            .opacity(isEnabled ? 1 : 0.45)
    }
}
