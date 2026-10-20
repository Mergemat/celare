import AppKit
import SwiftUI

/// Deep blue is the primary color. Everything interactive is convex: a
/// top-lit gradient, a slight border and a soft shadow, pressing in when
/// held. Surfaces that only show information stay flat, so the shape alone
/// tells you what can be clicked.
enum Palette {
    static let blueLight = Color(red: 0.25, green: 0.40, blue: 0.80)
    static let blue = Color(red: 0.11, green: 0.24, blue: 0.62)
    static let blueDark = Color(red: 0.05, green: 0.13, blue: 0.40)

    static let neutralLight = adaptive(light: (0.99, 0.99, 1.00), dark: (0.29, 0.30, 0.33))
    static let neutralDark = adaptive(light: (0.85, 0.86, 0.89), dark: (0.15, 0.16, 0.18))

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
        let top = prominent ? Palette.blueLight : Palette.neutralLight
        let bottom = prominent ? Palette.blueDark : Palette.neutralDark
        let shape = RoundedRectangle(cornerRadius: radius, style: .continuous)
        content
            .foregroundStyle(prominent ? Color.white : Color.primary)
            .background(
                shape.fill(LinearGradient(colors: pressed ? [bottom, top] : [top, bottom], startPoint: .top, endPoint: .bottom))
            )
            .overlay(
                shape.strokeBorder(
                    LinearGradient(colors: [.white.opacity(prominent ? 0.35 : 0.6), .black.opacity(0.22)], startPoint: .top, endPoint: .bottom),
                    lineWidth: 1
                )
            )
            .shadow(color: .black.opacity(pressed ? 0.08 : 0.22), radius: pressed ? 0.5 : 2, y: pressed ? 0 : 1.5)
            .scaleEffect(pressed ? 0.985 : 1)
            .animation(.easeOut(duration: 0.08), value: pressed)
    }
}

struct ConvexButtonStyle: ButtonStyle {
    var prominent = false
    var radius: CGFloat = 8

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 12, weight: .medium))
            .padding(.horizontal, 12)
            .padding(.vertical, 6)
            .modifier(Convex(prominent: prominent, pressed: configuration.isPressed, radius: radius))
            .contentShape(Rectangle())
    }
}

/// Rows in a list: full width, convex, blue when active.
struct ConvexRowStyle: ButtonStyle {
    var active: Bool

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .padding(.horizontal, 10)
            .padding(.vertical, 7)
            .frame(maxWidth: .infinity, alignment: .leading)
            .modifier(Convex(prominent: active, pressed: configuration.isPressed, radius: 8))
            .contentShape(Rectangle())
    }
}

/// The round power control in the header.
struct PowerButtonStyle: ButtonStyle {
    var on: Bool

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 18, weight: .semibold))
            .frame(width: 44, height: 44)
            .modifier(Convex(prominent: on, pressed: configuration.isPressed, radius: 22))
    }
}
