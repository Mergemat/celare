import AppKit
import SwiftUI

/// The design has one loud thing: the deep-blue convex bar that carries
/// the main action. Everything else is quiet. Secondary controls sit flat
/// and rise convex under the pointer, so what's clickable still reveals
/// itself by shape.
enum Palette {
    static let blueTop = Color(red: 0.25, green: 0.42, blue: 0.86)
    static let blueBottom = Color(red: 0.09, green: 0.20, blue: 0.55)
    static let blueDot = Color(red: 0.30, green: 0.50, blue: 0.95)

    static let raisedTop = adaptive(light: (1.00, 1.00, 1.00, 0.95), dark: (1, 1, 1, 0.13))
    static let raisedBottom = adaptive(light: (0.93, 0.94, 0.96, 0.95), dark: (1, 1, 1, 0.05))

    private static func adaptive(light: (CGFloat, CGFloat, CGFloat, CGFloat), dark: (CGFloat, CGFloat, CGFloat, CGFloat)) -> Color {
        Color(nsColor: NSColor(name: nil) { appearance in
            let c = appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua ? dark : light
            return NSColor(srgbRed: c.0, green: c.1, blue: c.2, alpha: c.3)
        })
    }
}

/// A convex surface: top-lit gradient, hairline border, inner highlight
/// along the top edge, tight shadow.
struct ConvexSurface: View {
    var blue: Bool
    var pressed: Bool
    var radius: CGFloat

    var body: some View {
        let shape = RoundedRectangle(cornerRadius: radius, style: .continuous)
        let top = blue ? Palette.blueTop : Palette.raisedTop
        let bottom = blue ? Palette.blueBottom : Palette.raisedBottom
        shape
            .fill(LinearGradient(colors: pressed ? [bottom, top] : [top, bottom], startPoint: .top, endPoint: .bottom))
            .overlay(
                // The lit top edge that makes it read as raised.
                shape.inset(by: 0.75).stroke(
                    LinearGradient(colors: [.white.opacity(blue ? 0.35 : 0.5), .clear], startPoint: .top, endPoint: .center),
                    lineWidth: 0.75)
            )
            .overlay(shape.stroke(Color.black.opacity(blue ? 0.35 : 0.12), lineWidth: 0.5))
            .shadow(color: .black.opacity(pressed ? 0.05 : 0.22), radius: pressed ? 0.5 : 1.5, y: pressed ? 0 : 1)
    }
}

/// The main action. Always raised, always deep blue.
struct PrimaryBarStyle: ButtonStyle {
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .labelStyle(BarLabelStyle())
            .font(.system(size: 12.5, weight: .semibold))
            .foregroundStyle(.white)
            .frame(maxWidth: .infinity)
            .frame(height: 30)
            .background(ConvexSurface(blue: true, pressed: configuration.isPressed, radius: 8))
            .opacity(isEnabled ? 1 : 0.5)
            .contentShape(Rectangle())
    }
}

/// A secondary button that is always raised (Revert, next to Keep).
struct RaisedButtonStyle: ButtonStyle {
    var height: CGFloat = 30

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: height < 30 ? 11 : 12.5, weight: .medium))
            .padding(.horizontal, height < 30 ? 10 : 12)
            .frame(height: height)
            .background(ConvexSurface(blue: false, pressed: configuration.isPressed, radius: 8))
            .contentShape(Rectangle())
    }
}

/// Flat at rest, convex under the pointer: rows and footer actions.
struct HoverRaiseStyle: ButtonStyle {
    var radius: CGFloat = 6
    var horizontal: CGFloat = 8
    var vertical: CGFloat = 5

    func makeBody(configuration: Configuration) -> some View {
        HoverRaise(configuration: configuration, radius: radius, horizontal: horizontal, vertical: vertical)
    }

    private struct HoverRaise: View {
        let configuration: Configuration
        let radius: CGFloat
        let horizontal: CGFloat
        let vertical: CGFloat
        @State private var hovering = false
        @Environment(\.isEnabled) private var isEnabled

        var body: some View {
            configuration.label
                .padding(.horizontal, horizontal)
                .padding(.vertical, vertical)
                .background(
                    ConvexSurface(blue: false, pressed: configuration.isPressed, radius: radius)
                        .opacity(hovering || configuration.isPressed ? 1 : 0)
                )
                .opacity(isEnabled ? 1 : 0.45)
                .contentShape(Rectangle())
                .onHover { hovering = $0 && isEnabled }
                .animation(.easeOut(duration: 0.1), value: hovering)
        }
    }
}

extension Font {
    static let mono = Font.system(size: 10.5, weight: .regular, design: .monospaced)
}

/// Icon and title for the primary bar, the icon a touch lighter.
struct BarLabelStyle: LabelStyle {
    func makeBody(configuration: Configuration) -> some View {
        HStack(spacing: 6) {
            configuration.icon.font(.system(size: 11.5, weight: .bold)).opacity(0.9)
            configuration.title
        }
    }
}

/// A spinner. ImageRenderer can't draw ProgressView, so snapshots get a
/// still glyph in its place.
struct Spinner: View {
    var size: ControlSize = .mini
    @Environment(\.snapshot) private var snapshot

    var body: some View {
        if snapshot {
            Image(systemName: "circle.dotted").font(.system(size: size == .mini ? 11 : 14, weight: .semibold))
        } else {
            ProgressView().controlSize(size)
        }
    }
}

extension EnvironmentValues {
    @Entry var snapshot = false
}
