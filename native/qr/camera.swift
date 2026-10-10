import AVFoundation
import CoreVideo
import Foundation
import Vision

private let cameraMaxWidth = 1280
private let cameraMaxHeight = 720
private let cameraMaxBufferBytes = 4 * 1024 * 1024
private let cameraMaxDevices = 64
private let cameraMaxStringBytes = 256
private let cameraMaxJSONBytes = 64 * 1024
private let cameraLifetimeSeconds: TimeInterval = 25

private struct CameraDescription: Codable {
    let uniqueID: String
    let localizedName: String
}

private func cameraDiscovery() throws -> AVCaptureDevice.DiscoverySession {
    guard #available(macOS 14.0, *) else {
        throw QRFailure.invalid("camera capture requires macOS 14 or later")
    }
    return AVCaptureDevice.DiscoverySession(
        deviceTypes: [.builtInWideAngleCamera, .external],
        mediaType: .video,
        position: .unspecified
    )
}

func listCameras() throws -> Data {
    let devices = try cameraDiscovery().devices
    guard devices.count <= cameraMaxDevices else {
        throw QRFailure.invalid("too many camera devices")
    }
    var seen = Set<String>()
    let descriptions = try devices.map { device -> CameraDescription in
        let identifier = device.uniqueID
        let name = device.localizedName
        guard !identifier.isEmpty,
              identifier.utf8.count <= cameraMaxStringBytes,
              name.utf8.count <= cameraMaxStringBytes,
              !identifier.utf8.contains(0), !name.utf8.contains(0),
              seen.insert(identifier).inserted else {
            throw QRFailure.invalid("camera discovery returned invalid or duplicate device details")
        }
        return CameraDescription(uniqueID: identifier, localizedName: name)
    }
    let json = try JSONEncoder().encode(descriptions)
    guard json.count <= cameraMaxJSONBytes else {
        throw QRFailure.invalid("camera listing exceeds its output bound")
    }
    return json
}

private final class CameraAuthorization {
    private let lock = NSLock()
    private var granted = false

    func set(_ value: Bool) {
        lock.lock()
        granted = value
        lock.unlock()
    }

    func value() -> Bool {
        lock.lock()
        defer { lock.unlock() }
        return granted
    }
}

private func authorizeCamera(until deadline: DispatchTime) throws {
    switch AVCaptureDevice.authorizationStatus(for: .video) {
    case .authorized:
        return
    case .notDetermined:
        let completed = DispatchSemaphore(value: 0)
        let authorization = CameraAuthorization()
        AVCaptureDevice.requestAccess(for: .video) { granted in
            authorization.set(granted)
            completed.signal()
        }
        guard completed.wait(timeout: deadline) == .success else {
            throw QRFailure.system("camera authorization timed out")
        }
        guard authorization.value() else {
            throw QRFailure.invalid("camera access was denied")
        }
    case .denied:
        throw QRFailure.invalid("camera access is denied; grant camera permission and retry")
    case .restricted:
        throw QRFailure.invalid("camera access is restricted")
    @unknown default:
        throw QRFailure.invalid("camera authorization state is unsupported")
    }
}

private final class QRFrameCollector: NSObject, AVCaptureVideoDataOutputSampleBufferDelegate {
    private let lock = NSLock()
    private let completed = DispatchSemaphore(value: 0)
    private var terminal = false
    private var terminalError: Error?
    private var commonDigest: String?
    private var commonCount: Int?
    private var commonTotal: Int?
    private var parts: [Int: QRPart] = [:]

    private var isTerminal: Bool {
        lock.lock()
        defer { lock.unlock() }
        return terminal
    }

    func wait(until deadline: DispatchTime) throws -> [QRPart] {
        guard completed.wait(timeout: deadline) == .success else {
            throw QRFailure.system("camera capture timed out")
        }
        lock.lock()
        defer { lock.unlock() }
        if let terminalError { throw terminalError }
        return parts.keys.sorted().compactMap { parts[$0] }
    }

    func cancel() {
        lock.lock()
        terminal = true
        lock.unlock()
    }

    private func finish(error: Error) {
        lock.lock()
        guard !terminal else {
            lock.unlock()
            return
        }
        terminal = true
        terminalError = error
        lock.unlock()
        completed.signal()
    }

    private func accept(_ part: QRPart) {
        lock.lock()
        defer { lock.unlock() }
        guard !terminal else { return }

        if let commonDigest {
            guard part.digest == commonDigest,
                  part.count == commonCount,
                  part.total == commonTotal else {
                terminal = true
                terminalError = QRFailure.invalid("camera saw mixed QR bundle sequences")
                completed.signal()
                return
            }
        } else {
            commonDigest = part.digest
            commonCount = part.count
            commonTotal = part.total
        }

        if let previous = parts[part.index] {
            guard previous.bytes == part.bytes else {
                terminal = true
                terminalError = QRFailure.invalid("camera saw conflicting duplicate QR parts")
                completed.signal()
                return
            }
            return
        }
        parts[part.index] = part
        if parts.count == part.count {
            terminal = true
            completed.signal()
        }
    }

    func captureOutput(_ output: AVCaptureOutput, didOutput sampleBuffer: CMSampleBuffer, from connection: AVCaptureConnection) {
        guard !isTerminal else { return }
        guard let pixelBuffer = CMSampleBufferGetImageBuffer(sampleBuffer) else { return }
        let width = CVPixelBufferGetWidth(pixelBuffer)
        let height = CVPixelBufferGetHeight(pixelBuffer)
        let rowBytes = CVPixelBufferGetBytesPerRow(pixelBuffer)
        let dataBytes = CVPixelBufferGetDataSize(pixelBuffer)
        guard CVPixelBufferGetPixelFormatType(pixelBuffer) == kCVPixelFormatType_32BGRA,
              width > 0, width <= cameraMaxWidth, height > 0, height <= cameraMaxHeight,
              rowBytes >= width * 4, rowBytes <= cameraMaxBufferBytes / height,
              dataBytes >= rowBytes * height, dataBytes <= cameraMaxBufferBytes else {
            finish(error: QRFailure.invalid("camera produced an unsupported frame format or size"))
            return
        }

        do {
            let request = VNDetectBarcodesRequest()
            request.symbologies = [.qr]
            try VNImageRequestHandler(cvPixelBuffer: pixelBuffer, orientation: .up).perform([request])
            let observations = request.results ?? []
            if observations.isEmpty { return }
            guard observations.count == 1,
                  let text = observations[0].payloadStringValue,
                  text.utf8.allSatisfy({ $0 < 128 }) else {
                finish(error: QRFailure.invalid("camera frame must contain exactly one ASCII QR code"))
                return
            }
            accept(try parsePart(Data(text.utf8)))
        } catch {
            finish(error: error)
        }
    }
}

func captureParts(deviceID: String) throws -> [QRPart] {
    guard #available(macOS 14.0, *) else {
        throw QRFailure.invalid("camera capture requires macOS 14 or later")
    }
    guard !deviceID.isEmpty, deviceID.utf8.count <= cameraMaxStringBytes,
          !deviceID.utf8.contains(0) else {
        throw QRFailure.invalid("camera ID must be a bounded exact device identifier")
    }
    let deadline = DispatchTime.now() + cameraLifetimeSeconds
    try authorizeCamera(until: deadline)

    let devices = try cameraDiscovery().devices
    guard devices.count <= cameraMaxDevices else {
        throw QRFailure.invalid("too many camera devices")
    }
    let matches = devices.filter { $0.uniqueID == deviceID }
    guard matches.count == 1, let device = matches.first else {
        throw QRFailure.invalid("selected camera is unavailable; choose an exact ID from qr-tool cameras")
    }
    let input: AVCaptureDeviceInput
    do {
        input = try AVCaptureDeviceInput(device: device)
    } catch {
        throw QRFailure.system("cannot open selected camera input")
    }

    let session = AVCaptureSession()
    let output = AVCaptureVideoDataOutput()
    output.alwaysDiscardsLateVideoFrames = true
    let callbackQueue = DispatchQueue(label: "org.airc.qr.camera.frames", autoreleaseFrequency: .workItem)
    let sessionQueue = DispatchQueue(label: "org.airc.qr.camera.session")
    let collector = QRFrameCollector()
    output.setSampleBufferDelegate(collector, queue: callbackQueue)

    session.beginConfiguration()
    do {
        guard session.canAddInput(input) else {
            throw QRFailure.invalid("selected camera input cannot be added")
        }
        session.addInput(input)
        guard session.canSetSessionPreset(.hd1280x720) else {
            throw QRFailure.invalid("selected camera does not support the bounded 1280x720 profile")
        }
        session.sessionPreset = .hd1280x720
        guard session.canAddOutput(output) else {
            throw QRFailure.invalid("selected camera cannot provide bounded video frames")
        }
        session.addOutput(output)
        guard output.availableVideoPixelFormatTypes.contains(kCVPixelFormatType_32BGRA) else {
            throw QRFailure.invalid("selected camera cannot provide BGRA video frames")
        }
        output.videoSettings = [kCVPixelBufferPixelFormatTypeKey as String: Int(kCVPixelFormatType_32BGRA)]
        session.commitConfiguration()
    } catch {
        if session.outputs.contains(where: { $0 === output }) { session.removeOutput(output) }
        if session.inputs.contains(where: { $0 === input }) { session.removeInput(input) }
        session.commitConfiguration()
        output.setSampleBufferDelegate(nil, queue: nil)
        callbackQueue.sync {}
        throw error
    }

    defer {
        collector.cancel()
        sessionQueue.sync {
            if session.isRunning { session.stopRunning() }
        }
        output.setSampleBufferDelegate(nil, queue: nil)
        callbackQueue.sync {}
        sessionQueue.sync {
            session.beginConfiguration()
            if session.outputs.contains(where: { $0 === output }) { session.removeOutput(output) }
            if session.inputs.contains(where: { $0 === input }) { session.removeInput(input) }
            session.commitConfiguration()
        }
    }

    let started = DispatchSemaphore(value: 0)
    sessionQueue.async {
        session.startRunning()
        started.signal()
    }
    guard started.wait(timeout: deadline) == .success, session.isRunning else {
        throw QRFailure.system("camera failed to start before the capture deadline")
    }
    return try collector.wait(until: deadline)
}
