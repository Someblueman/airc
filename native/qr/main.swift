import CoreGraphics
import CoreImage
import CryptoKit
import Foundation
import ImageIO
import Vision
import Darwin

private let chunkSize = 512
private let maxBundleBytes = 65_536
private let maxImages = 128
private let maxPNGBytes = 4 * 1024 * 1024
private let maxImageDimension = 2_048

private enum QRFailure: Error, CustomStringConvertible {
    case usage
    case invalid(String)
    case system(String)

    var description: String {
        switch self {
        case .usage: return "usage: qr-tool encode --input ABS_BUNDLE --output FRESH_DIRECTORY | qr-tool decode --output NEW_BUNDLE ABS_PNG..."
        case .invalid(let message), .system(let message): return message
        }
    }
}

private func fail(_ message: String) -> Never {
    fputs("qr-tool: \(message)\n", stderr)
    exit(1)
}

private func normalizedAbsolutePath(_ value: String) throws -> String {
    let components = value.split(separator: "/", omittingEmptySubsequences: false)
    guard value.hasPrefix("/"), value != "/", !value.utf8.contains(0),
          components.count >= 2, components.first == "",
          components.dropFirst().allSatisfy({ !$0.isEmpty && $0 != "." && $0 != ".." }) else {
        throw QRFailure.invalid("path must be absolute and normalized")
    }
    return value
}

private func writeAll(_ fd: Int32, _ bytes: Data) throws {
    try bytes.withUnsafeBytes { raw in
        guard let base = raw.baseAddress else { return }
        var offset = 0
        while offset < raw.count {
            let n = Darwin.write(fd, base.advanced(by: offset), raw.count - offset)
            if n < 0 && errno == EINTR { continue }
            guard n > 0 else { throw QRFailure.system("write failed: \(String(cString: strerror(errno)))") }
            offset += n
        }
    }
}

private func readRegularFile(_ path: String, limit: Int) throws -> Data {
    var before = stat()
    guard lstat(path, &before) == 0, (before.st_mode & S_IFMT) == S_IFREG,
          before.st_size >= 0, before.st_size <= limit else {
        throw QRFailure.invalid("input must be a bounded regular file")
    }
    let fd = Darwin.open(path, O_RDONLY | O_NOFOLLOW | O_NONBLOCK | O_CLOEXEC)
    guard fd >= 0 else { throw QRFailure.system("cannot open input: \(String(cString: strerror(errno)))") }
    defer { _ = Darwin.close(fd) }
    var info = stat()
    guard fstat(fd, &info) == 0, (info.st_mode & S_IFMT) == S_IFREG,
          info.st_dev == before.st_dev, info.st_ino == before.st_ino,
          info.st_size >= 0, info.st_size <= limit else {
        throw QRFailure.invalid("input must be a bounded regular file")
    }
    var data = Data(count: Int(info.st_size))
    try data.withUnsafeMutableBytes { raw in
        guard let base = raw.baseAddress else { return }
        var offset = 0
        while offset < raw.count {
            let n = Darwin.read(fd, base.advanced(by: offset), raw.count - offset)
            if n < 0 && errno == EINTR { continue }
            guard n > 0 else { throw QRFailure.system("short input read") }
            offset += n
        }
    }
    var extra: UInt8 = 0
    var extraCount: Int
    repeat {
        extraCount = Darwin.read(fd, &extra, 1)
    } while extraCount < 0 && errno == EINTR
    guard extraCount == 0 else { throw QRFailure.invalid("input changed while reading or exceeds its size bound") }
    return data
}

private func sha256Hex(_ data: Data) -> String {
    SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
}

private func renderQR(_ text: String) throws -> Data {
    guard let input = text.data(using: .ascii) else { throw QRFailure.invalid("QR text is not ASCII") }
    guard let filter = CIFilter(name: "CIQRCodeGenerator") else {
        throw QRFailure.system("CoreImage QR encoder is unavailable")
    }
    filter.setValue(input, forKey: "inputMessage")
    filter.setValue("M", forKey: "inputCorrectionLevel")
    guard let code = filter.outputImage else { throw QRFailure.system("CoreImage could not encode QR text") }
    let extent = code.extent.integral
    let available = min(maxImageDimension / Int(extent.width) - 1,
                        maxImageDimension / Int(extent.height) - 1)
    let scale = min(12, available)
    guard scale >= 1 else { throw QRFailure.invalid("QR payload exceeds image bounds") }
    let quiet = CGFloat(4 * scale)
    let scaled = code.transformed(by: CGAffineTransform(scaleX: CGFloat(scale), y: CGFloat(scale)))
        .transformed(by: CGAffineTransform(translationX: quiet, y: quiet))
    let canvas = CGRect(x: 0, y: 0,
                        width: extent.width * CGFloat(scale) + quiet * 2,
                        height: extent.height * CGFloat(scale) + quiet * 2)
    guard canvas.width <= CGFloat(maxImageDimension), canvas.height <= CGFloat(maxImageDimension) else {
        throw QRFailure.invalid("QR payload exceeds image bounds")
    }
    let image = CIImage(color: CIColor.white).cropped(to: canvas)
    let composed = scaled.composited(over: image).cropped(to: canvas)
    let context = CIContext(options: [.useSoftwareRenderer: true])
    guard let cgImage = context.createCGImage(composed, from: canvas) else {
        throw QRFailure.system("CoreImage could not render QR image")
    }
    let output = NSMutableData()
    guard let destination = CGImageDestinationCreateWithData(output, "public.png" as CFString, 1, nil) else {
        throw QRFailure.system("ImageIO could not create PNG")
    }
    CGImageDestinationAddImage(destination, cgImage, nil)
    guard CGImageDestinationFinalize(destination), output.length <= maxPNGBytes else {
        throw QRFailure.system("PNG encoding failed or exceeded image bound")
    }
    return output as Data
}

private func encode(bundlePath: String, outputPath: String) throws {
    let input = try normalizedAbsolutePath(bundlePath)
    let output = try normalizedAbsolutePath(outputPath)
    let payload = try readRegularFile(input, limit: maxBundleBytes)
    guard !payload.isEmpty else { throw QRFailure.invalid("bundle must contain 1..65536 bytes") }
    let count = (payload.count + chunkSize - 1) / chunkSize
    guard count <= maxImages else { throw QRFailure.invalid("bundle requires too many images") }
    guard mkdir(output, 0o700) == 0 else {
        throw QRFailure.system("output directory must be fresh: \(String(cString: strerror(errno)))")
    }
    let dirfd = Darwin.open(output, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC)
    guard dirfd >= 0 else { throw QRFailure.system("cannot open output directory") }
    defer { _ = Darwin.close(dirfd) }
    var dirInfo = stat()
    guard fstat(dirfd, &dirInfo) == 0, (dirInfo.st_mode & S_IFMT) == S_IFDIR,
          dirInfo.st_uid == geteuid(), (dirInfo.st_mode & 0o077) == 0 else {
        throw QRFailure.invalid("output directory is not private")
    }
    let digest = sha256Hex(payload)
    for index in 0..<count {
        let start = index * chunkSize
        let end = min(start + chunkSize, payload.count)
        let chunk = payload.subdata(in: start..<end)
        let text = "AIRCQR1:\(digest):\(count):\(index):\(payload.count):\(chunk.base64EncodedString())"
        let png = try autoreleasepool { try renderQR(text) }
        let name = String(format: "%04d.png", index)
        let fd = name.withCString { openat(dirfd, $0, O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW | O_CLOEXEC, 0o600) }
        guard fd >= 0 else { throw QRFailure.system("cannot create QR image: \(String(cString: strerror(errno)))") }
        var fdOpen = true
        defer {
            if fdOpen { _ = close(fd) }
        }
        try writeAll(fd, png)
        guard fsync(fd) == 0 else { throw QRFailure.system("cannot sync QR image") }
        let closeResult = close(fd)
        fdOpen = false
        guard closeResult == 0 else { throw QRFailure.system("cannot close QR image") }
    }
}

private struct QRPart {
    let digest: String
    let count: Int
    let index: Int
    let total: Int
    let bytes: Data
}

private func canonicalDecimal(_ text: String, max: Int) throws -> Int {
    guard !text.isEmpty, text.utf8.allSatisfy({ $0 >= 48 && $0 <= 57 }),
          let value = Int(text), value <= max, String(value) == text else {
        throw QRFailure.invalid("QR sequence has a noncanonical number")
    }
    return value
}

private func inspectPNG(_ path: String) throws -> Data {
    let png = try readRegularFile(path, limit: maxPNGBytes)
    guard let source = CGImageSourceCreateWithData(png as CFData, nil),
          CGImageSourceGetType(source) == "public.png" as CFString,
          CGImageSourceGetCount(source) == 1,
          let properties = CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [CFString: Any],
          let width = properties[kCGImagePropertyPixelWidth] as? Int,
          let height = properties[kCGImagePropertyPixelHeight] as? Int,
          (1...maxImageDimension).contains(width), (1...maxImageDimension).contains(height) else {
        throw QRFailure.invalid("input must be a single bounded PNG image")
    }
    // A one-frame APNG also has image count 1. Its animation metadata belongs
    // to the container's PNG dictionary, not the top-level image properties.
    let container = CGImageSourceCopyProperties(source, nil) as? [CFString: Any]
    let pngProperties = container?[kCGImagePropertyPNGDictionary] as? [CFString: Any]
    guard pngProperties?[kCGImagePropertyAPNGFrameInfoArray] == nil,
          pngProperties?[kCGImagePropertyAPNGLoopCount] == nil,
          let image = CGImageSourceCreateImageAtIndex(source, 0, nil) else {
        throw QRFailure.invalid("input must be a static PNG image")
    }
    guard image.width == width, image.height == height else {
        throw QRFailure.invalid("PNG dimensions changed during decode")
    }
    let request = VNDetectBarcodesRequest()
    request.symbologies = [.qr]
    try VNImageRequestHandler(cgImage: image).perform([request])
    guard let observations = request.results, observations.count == 1,
          let text = observations[0].payloadStringValue,
          text.utf8.allSatisfy({ $0 < 128 }) else {
        throw QRFailure.invalid("PNG must contain exactly one ASCII QR code")
    }
    return Data(text.utf8)
}

private func parsePart(_ data: Data) throws -> QRPart {
    guard data.count <= 800, let text = String(data: data, encoding: .ascii) else {
        throw QRFailure.invalid("QR payload is oversized or not ASCII")
    }
    let fields = text.split(separator: ":", omittingEmptySubsequences: false)
    guard fields.count == 6, fields[0] == "AIRCQR1" else { throw QRFailure.invalid("invalid QR sequence header") }
    let digest = String(fields[1])
    guard digest.count == 64, digest.utf8.allSatisfy({ ($0 >= 48 && $0 <= 57) || ($0 >= 97 && $0 <= 102) }) else {
        throw QRFailure.invalid("invalid QR sequence digest")
    }
    let count = try canonicalDecimal(String(fields[2]), max: maxImages)
    let index = try canonicalDecimal(String(fields[3]), max: maxImages - 1)
    let total = try canonicalDecimal(String(fields[4]), max: maxBundleBytes)
    guard total > 0, count > 0, count == (total + chunkSize - 1) / chunkSize,
          index < count,
          let chunk = Data(base64Encoded: String(fields[5]), options: []),
          chunk.base64EncodedString() == String(fields[5]) else {
        throw QRFailure.invalid("invalid QR chunk framing or noncanonical base64")
    }
    let expectedLength = index == count - 1 ? total - index * chunkSize : chunkSize
    guard chunk.count == expectedLength else { throw QRFailure.invalid("invalid QR chunk length") }
    return QRPart(digest: digest, count: count, index: index, total: total, bytes: chunk)
}

private func publish(_ data: Data, at outputPath: String) throws {
    let output = try normalizedAbsolutePath(outputPath)
    let parent = URL(fileURLWithPath: output).deletingLastPathComponent().path
    let name = URL(fileURLWithPath: output).lastPathComponent
    guard !name.isEmpty, name != ".", name != ".." else { throw QRFailure.invalid("invalid output filename") }
    var resolvedParent = [CChar](repeating: 0, count: Int(PATH_MAX))
    guard parent.withCString({ realpath($0, &resolvedParent) }) != nil else {
        throw QRFailure.system("cannot resolve output parent directory")
    }
    let dirfd = Darwin.open(String(cString: resolvedParent), O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC)
    guard dirfd >= 0 else { throw QRFailure.system("cannot open output parent directory") }
    defer { _ = Darwin.close(dirfd) }
    var parentInfo = stat()
    guard fstat(dirfd, &parentInfo) == 0, (parentInfo.st_mode & S_IFMT) == S_IFDIR,
          parentInfo.st_uid == geteuid(), (parentInfo.st_mode & 0o022) == 0 else {
        throw QRFailure.invalid("output parent must be owned by the current user and not group/other writable")
    }
    let tempName = ".airc-qr-\(UUID().uuidString).tmp"
    let fd = tempName.withCString { openat(dirfd, $0, O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW | O_CLOEXEC, 0o600) }
    guard fd >= 0 else { throw QRFailure.system("cannot create output temporary file") }
    var keepTemp = true
    defer {
        _ = close(fd)
        if keepTemp { _ = tempName.withCString { unlinkat(dirfd, $0, 0) } }
    }
    try writeAll(fd, data)
    guard fsync(fd) == 0 else { throw QRFailure.system("cannot sync decoded bundle") }
    guard tempName.withCString({ source in name.withCString { destination in linkat(dirfd, source, dirfd, destination, 0) } }) == 0 else {
        throw QRFailure.system("cannot publish decoded bundle without overwrite: \(String(cString: strerror(errno)))")
    }
    guard tempName.withCString({ unlinkat(dirfd, $0, 0) }) == 0 else {
        throw QRFailure.system("cannot remove publication temporary file")
    }
    keepTemp = false
}

private func assembleParts(_ values: [QRPart]) throws -> Data {
    guard !values.isEmpty, values.count <= maxImages else { throw QRFailure.invalid("supply 1..128 QR parts") }
    var parts: [Int: QRPart] = [:]
    var commonDigest: String?
    var commonCount: Int?
    var commonTotal: Int?
    for part in values {
        if let digest = commonDigest, digest != part.digest { throw QRFailure.invalid("mixed QR sequences") }
        if let count = commonCount, count != part.count { throw QRFailure.invalid("mixed QR sequences") }
        if let total = commonTotal, total != part.total { throw QRFailure.invalid("mixed QR sequences") }
        commonDigest = part.digest
        commonCount = part.count
        commonTotal = part.total
        guard parts.updateValue(part, forKey: part.index) == nil else { throw QRFailure.invalid("duplicate QR chunk index") }
    }
    guard let count = commonCount, let total = commonTotal, let digest = commonDigest,
          count == values.count, parts.count == count else { throw QRFailure.invalid("missing QR chunks") }
    var payload = Data()
    payload.reserveCapacity(total)
    for index in 0..<count {
        guard let part = parts[index] else { throw QRFailure.invalid("missing QR chunk index") }
        payload.append(part.bytes)
    }
    guard payload.count == total, sha256Hex(payload) == digest else { throw QRFailure.invalid("QR sequence integrity check failed") }
    return payload
}

private func decode(outputPath: String, paths: [String]) throws {
    guard !paths.isEmpty, paths.count <= maxImages else { throw QRFailure.invalid("supply 1..128 PNG images") }
    let parts = try paths.map { path in
        try autoreleasepool { try parsePart(inspectPNG(normalizedAbsolutePath(path))) }
    }
    try publish(assembleParts(parts), at: outputPath)
}

private func run() throws {
    let args = Array(CommandLine.arguments.dropFirst())
    if args.count == 5, args[0] == "encode", args[1] == "--input", args[3] == "--output" {
        try encode(bundlePath: args[2], outputPath: args[4])
        return
    }
    if args.count >= 4, args[0] == "decode", args[1] == "--output" {
        try decode(outputPath: args[2], paths: Array(args.dropFirst(3)))
        return
    }
    throw QRFailure.usage
}

_ = Darwin.signal(SIGALRM, SIG_DFL)
_ = Darwin.signal(SIGINT, SIG_DFL)
_ = Darwin.signal(SIGTERM, SIG_DFL)
Darwin.alarm(30)
do {
    try run()
} catch {
    fail(String(describing: error))
}
