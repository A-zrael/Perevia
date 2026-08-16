#!/usr/bin/env python3

import base64
import binascii
import hmac
import json
import os
import queue
import signal
import sys
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import LXMF
import RNS


MAX_REQUEST_BODY = 2 * 1024 * 1024
MAX_AUDIO_BYTES = 1024 * 1024
MAX_IMAGE_BYTES = 1024 * 1024
STATE_NAMES = {
    LXMF.LXMessage.GENERATING: "queued",
    LXMF.LXMessage.OUTBOUND: "queued",
    LXMF.LXMessage.SENDING: "sending",
    LXMF.LXMessage.SENT: "sent",
    LXMF.LXMessage.DELIVERED: "delivered",
    LXMF.LXMessage.REJECTED: "failed",
    LXMF.LXMessage.CANCELLED: "failed",
    LXMF.LXMessage.FAILED: "failed",
}


def env_bool(name, fallback=False):
    value = os.getenv(name)
    if value is None:
        return fallback
    return value.strip().lower() in {"1", "true", "yes", "on"}


class EventHub:
    def __init__(self):
        self._subscribers = set()
        self._lock = threading.Lock()

    def subscribe(self):
        subscriber = queue.Queue(maxsize=128)
        with self._lock:
            self._subscribers.add(subscriber)
        return subscriber

    def unsubscribe(self, subscriber):
        with self._lock:
            self._subscribers.discard(subscriber)

    def publish(self, event_type, payload):
        event = {"type": event_type, "timestamp": time.time(), "data": payload}
        with self._lock:
            subscribers = tuple(self._subscribers)
        for subscriber in subscribers:
            try:
                subscriber.put_nowait(event)
            except queue.Full:
                try:
                    subscriber.get_nowait()
                    subscriber.put_nowait(event)
                except (queue.Empty, queue.Full):
                    pass


class DeliveryAnnounceHandler:
    aspect_filter = "lxmf.delivery"
    receive_path_responses = True

    def __init__(self, integration):
        self.integration = integration

    def received_announce(
        self,
        destination_hash,
        announced_identity,
        app_data,
        announce_packet_hash=None,
        is_path_response=False,
    ):
        try:
            display_name = LXMF.display_name_from_app_data(app_data)
        except Exception:
            display_name = None
        self.integration.events.publish(
            "announce",
            {
                "destination": destination_hash.hex(),
                "display_name": display_name,
                "hops": RNS.Transport.hops_to(destination_hash),
                "path_response": bool(is_path_response),
                "identity_hash": announced_identity.hash.hex() if announced_identity else None,
            },
        )


class LXMFIntegration:
    def __init__(self):
        self.events = EventHub()
        self.started_at = time.time()
        self.path_timeout = float(os.getenv("LXMF_PATH_TIMEOUT_SECONDS", "30"))
        self.data_dir = Path(os.getenv("LXMF_DATA_DIR", "/data"))
        self.rns_config_dir = Path(os.getenv("RNS_CONFIG_DIR", self.data_dir / "rns"))
        self.storage_dir = Path(os.getenv("LXMF_STORAGE_DIR", self.data_dir / "lxmf"))
        self.identity_path = Path(os.getenv("LXMF_IDENTITY_PATH", self.data_dir / "identity"))
        self.settings_path = self.data_dir / "settings.json"
        self.settings_lock = threading.Lock()
        self._prepare_directories()
        self.display_name = self._load_display_name()
        self._prepare_rns_config()

        self.reticulum = RNS.Reticulum(
            configdir=str(self.rns_config_dir),
            require_shared_instance=True,
            shared_instance_type="tcp",
        )
        if not self.reticulum.is_connected_to_shared_instance:
            raise RuntimeError("LXMF bridge did not connect to the required rnsd shared instance")

        self.identity = self._load_identity()
        self.router = LXMF.LXMRouter(storagepath=str(self.storage_dir))
        self.source = self.router.register_delivery_identity(
            self.identity,
            display_name=self.display_name,
        )
        self.router.register_delivery_callback(self._on_inbound)
        RNS.Transport.register_announce_handler(DeliveryAnnounceHandler(self))

        if env_bool("LXMF_ANNOUNCE_AT_START", True):
            self.router.announce(self.source.hash)

    def _prepare_directories(self):
        self.data_dir.mkdir(parents=True, exist_ok=True)
        self.rns_config_dir.mkdir(parents=True, exist_ok=True)
        self.storage_dir.mkdir(parents=True, exist_ok=True)

    def _prepare_rns_config(self):
        config_path = self.rns_config_dir / "config"
        shared_port = int(os.getenv("RNS_SHARED_INSTANCE_PORT", "37428"))
        control_port = int(os.getenv("RNS_INSTANCE_CONTROL_PORT", "37429"))
        rpc_key = os.getenv("RNS_RPC_KEY", "").strip()
        if not rpc_key:
            raise RuntimeError("RNS_RPC_KEY is required and must match rnsd's rpc_key")
        try:
            decoded_rpc_key = bytes.fromhex(rpc_key)
        except ValueError as error:
            raise RuntimeError("RNS_RPC_KEY must be hexadecimal") from error
        if len(decoded_rpc_key) < 16:
            raise RuntimeError("RNS_RPC_KEY must contain at least 16 bytes")
        config_path.write_text(
            "[reticulum]\n"
            "  share_instance = Yes\n"
            "  shared_instance_type = tcp\n"
            f"  shared_instance_port = {shared_port}\n"
            f"  instance_control_port = {control_port}\n"
            f"  rpc_key = {rpc_key}\n",
            encoding="utf-8",
        )
        config_path.chmod(0o600)

    def _load_display_name(self):
        fallback = os.getenv("LXMF_DISPLAY_NAME", "PEREVIA").strip()
        if not self.settings_path.exists():
            return fallback
        try:
            settings = json.loads(self.settings_path.read_text(encoding="utf-8"))
            saved_name = settings.get("display_name", "").strip()
            return fallback if saved_name == "Web Sideband" else (saved_name or fallback)
        except (OSError, ValueError, AttributeError):
            RNS.log("Could not load bridge settings; using configured display name", RNS.LOG_WARNING)
            return fallback

    def set_display_name(self, value):
        if not isinstance(value, str):
            raise ValueError("display_name must be text")
        value = value.strip()
        if not value or len(value) > 64 or len(value.encode("utf-8")) > 128:
            raise ValueError("display_name must contain 1 to 64 characters")
        with self.settings_lock:
            temporary_path = self.settings_path.with_suffix(".tmp")
            temporary_path.write_text(
                json.dumps({"display_name": value}, separators=(",", ":")),
                encoding="utf-8",
            )
            temporary_path.chmod(0o600)
            temporary_path.replace(self.settings_path)
            self.display_name = value
            self.source.display_name = value
        self.announce()
        self.events.publish("identity_updated", self.status())
        return self.status()

    def _load_identity(self):
        if self.identity_path.exists():
            identity = RNS.Identity.from_file(str(self.identity_path))
            if identity is None:
                raise RuntimeError("persistent identity file is invalid")
            return identity
        identity = RNS.Identity()
        if not identity.to_file(str(self.identity_path)):
            raise RuntimeError("could not persist new LXMF identity")
        self.identity_path.chmod(0o600)
        return identity

    def status(self):
        return {
            "connected": bool(self.reticulum.is_connected_to_shared_instance),
            "address": self.source.hash.hex(),
            "display_name": self.display_name,
            "shared_instance": {
                "type": "tcp",
                "port": int(os.getenv("RNS_SHARED_INSTANCE_PORT", "37428")),
                "control_port": int(os.getenv("RNS_INSTANCE_CONTROL_PORT", "37429")),
                "control_port_used": True,
                "rpc_authenticated": True,
            },
            "uptime_seconds": int(time.time() - self.started_at),
        }

    def announce(self):
        self.router.announce(self.source.hash)
        self.events.publish("local_announce", {"destination": self.source.hash.hex()})

    def send(self, destination_hex, content, title="", method="direct", audio=None, image=None):
        if not isinstance(destination_hex, str) or len(destination_hex) != 32:
            raise ValueError("destination must be a 32-character LXMF address")
        if not isinstance(content, str) or not content.strip():
            raise ValueError("content must be a non-empty string")
        if not isinstance(title, str):
            raise ValueError("title must be a string")
        if len(content.encode("utf-8")) > 32 * 1024:
            raise ValueError("content exceeds proof-of-concept limit")

        fields = {}
        if audio is not None:
            if not isinstance(audio, dict) or audio.get("mode") != "opus_ogg":
                raise ValueError("audio must use the opus_ogg mode")
            encoded_audio = audio.get("data")
            if not isinstance(encoded_audio, str):
                raise ValueError("audio data must be base64 text")
            try:
                audio_data = base64.b64decode(encoded_audio, validate=True)
            except (binascii.Error, ValueError) as error:
                raise ValueError("audio data must be valid base64") from error
            if not audio_data or len(audio_data) > MAX_AUDIO_BYTES:
                raise ValueError("audio data has an invalid size")
            fields[LXMF.FIELD_AUDIO] = [LXMF.AM_OPUS_OGG, audio_data]
        if image is not None:
            if not isinstance(image, dict) or image.get("format") != "webp":
                raise ValueError("image must use the webp format")
            encoded_image = image.get("data")
            if not isinstance(encoded_image, str):
                raise ValueError("image data must be base64 text")
            try:
                image_data = base64.b64decode(encoded_image, validate=True)
            except (binascii.Error, ValueError) as error:
                raise ValueError("image data must be valid base64") from error
            if not image_data or len(image_data) > MAX_IMAGE_BYTES:
                raise ValueError("image data has an invalid size")
            fields[LXMF.FIELD_IMAGE] = ["webp", image_data]
        try:
            destination_hash = bytes.fromhex(destination_hex)
        except ValueError as error:
            raise ValueError("destination must be hexadecimal") from error

        if not RNS.Transport.has_path(destination_hash):
            RNS.Transport.request_path(destination_hash)
            deadline = time.time() + self.path_timeout
            while not RNS.Transport.has_path(destination_hash) and time.time() < deadline:
                time.sleep(0.1)

        recipient_identity = RNS.Identity.recall(destination_hash)
        if recipient_identity is None:
            raise TimeoutError("destination identity is unknown after path discovery")

        destination = RNS.Destination(
            recipient_identity,
            RNS.Destination.OUT,
            RNS.Destination.SINGLE,
            "lxmf",
            "delivery",
        )
        methods = {
            "direct": LXMF.LXMessage.DIRECT,
            "opportunistic": LXMF.LXMessage.OPPORTUNISTIC,
        }
        if method not in methods:
            raise ValueError("method must be direct or opportunistic")

        request_id = uuid.uuid4().hex
        message = LXMF.LXMessage(
            destination,
            self.source,
            content,
            title,
            desired_method=methods[method],
            fields=fields,
            include_ticket=True,
        )
        message.register_delivery_callback(
            lambda delivered: self._on_outbound_state(request_id, delivered)
        )
        message.register_failed_callback(
            lambda failed: self._on_outbound_state(request_id, failed)
        )
        self.events.publish(
            "message_status",
            {
                "request_id": request_id,
                "message_id": None,
                "destination": destination_hex.lower(),
                "state": "queued",
                "method": method,
            },
        )
        self.router.handle_outbound(message)
        self.events.publish(
            "message_status",
            self._outbound_payload(request_id, message, "sending"),
        )
        return {
            "request_id": request_id,
            "message_id": message.hash.hex() if message.hash else None,
            "state": STATE_NAMES.get(message.state, "queued"),
        }

    def _outbound_payload(self, request_id, message, state_override=None):
        return {
            "request_id": request_id,
            "message_id": message.hash.hex() if message.hash else None,
            "destination": message.destination_hash.hex(),
            "state": state_override or STATE_NAMES.get(message.state, "queued"),
            "method": self._method_name(message.method or message.desired_method),
            "progress": message.progress,
        }

    def _on_outbound_state(self, request_id, message):
        self.events.publish(
            "message_status",
            self._outbound_payload(request_id, message),
        )

    def _on_inbound(self, message):
        payload = {
                "message_id": message.hash.hex() if message.hash else None,
                "source": message.source_hash.hex(),
                "destination": message.destination_hash.hex(),
                "timestamp": message.timestamp,
                "title": message.title_as_string(),
                "content": message.content_as_string(),
                "method": self._method_name(message.method),
                "signature_validated": bool(message.signature_validated),
                "stamp_valid": bool(message.stamp_valid),
            }
        if message.fields and LXMF.FIELD_AUDIO in message.fields:
            audio_field = message.fields[LXMF.FIELD_AUDIO]
            if (
                isinstance(audio_field, (list, tuple))
                and len(audio_field) == 2
                and audio_field[0] == LXMF.AM_OPUS_OGG
                and isinstance(audio_field[1], bytes)
                and len(audio_field[1]) <= MAX_AUDIO_BYTES
            ):
                payload["audio"] = {
                    "mode": "opus_ogg",
                    "data": base64.b64encode(audio_field[1]).decode("ascii"),
                }
        if message.fields and LXMF.FIELD_IMAGE in message.fields:
            image_field = message.fields[LXMF.FIELD_IMAGE]
            if isinstance(image_field, (list, tuple)) and len(image_field) == 2:
                image_format = image_field[0]
                if isinstance(image_format, bytes):
                    image_format = image_format.decode("ascii", errors="ignore")
                image_format = str(image_format).lower().lstrip(".")
                image_data = image_field[1]
                if isinstance(image_data, bytearray):
                    image_data = bytes(image_data)
                if image_format in {"webp", "png", "jpg", "jpeg"} and isinstance(image_data, bytes) and 0 < len(image_data) <= MAX_IMAGE_BYTES:
                    payload["image"] = {
                        "format": image_format,
                        "data": base64.b64encode(image_data).decode("ascii"),
                    }
                else:
                    payload["image_error"] = "unsupported or oversized image field"
        self.events.publish("message_received", payload)

    @staticmethod
    def _method_name(method):
        return {
            LXMF.LXMessage.OPPORTUNISTIC: "opportunistic",
            LXMF.LXMessage.DIRECT: "direct",
            LXMF.LXMessage.PROPAGATED: "propagated",
            LXMF.LXMessage.PAPER: "paper",
        }.get(method, "unknown")


class BridgeHTTPServer(ThreadingHTTPServer):
    def handle_error(self, request, client_address):
        if isinstance(sys.exc_info()[1], ConnectionResetError):
            return
        super().handle_error(request, client_address)


class BridgeHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    integration = None
    api_token = ""

    def do_GET(self):
        if self.path != "/healthz" and not self._authorised():
            return
        if self.path == "/healthz":
            self._json(200, {"status": "ok"})
        elif self.path == "/v1/status":
            self._json(200, self.integration.status())
        elif self.path == "/v1/events":
            self._events()
        else:
            self._json(404, {"error": "not found"})

    def do_POST(self):
        if not self._authorised():
            return
        try:
            if self.path == "/v1/messages":
                payload = self._read_json()
                result = self.integration.send(
                    payload.get("destination"),
                    payload.get("content"),
                    payload.get("title", ""),
                    payload.get("method", "direct"),
                    payload.get("audio"),
                    payload.get("image"),
                )
                self._json(202, result)
            elif self.path == "/v1/announce":
                self.integration.announce()
                self._json(202, {"state": "announced"})
            else:
                self._json(404, {"error": "not found"})
        except ValueError as error:
            self._json(400, {"error": str(error)})
        except TimeoutError as error:
            self._json(504, {"error": str(error)})
        except Exception:
            RNS.log("LXMF bridge request failed", RNS.LOG_ERROR)
            self._json(500, {"error": "LXMF operation failed"})

    def do_PUT(self):
        if not self._authorised():
            return
        try:
            if self.path == "/v1/settings/identity":
                payload = self._read_json()
                self._json(200, self.integration.set_display_name(payload.get("display_name")))
            else:
                self._json(404, {"error": "not found"})
        except ValueError as error:
            self._json(400, {"error": str(error)})
        except Exception:
            RNS.log("LXMF bridge settings update failed", RNS.LOG_ERROR)
            self._json(500, {"error": "LXMF settings update failed"})

    def _authorised(self):
        if not self.api_token:
            return True
        expected = "Bearer " + self.api_token
        supplied = self.headers.get("Authorization", "")
        if hmac.compare_digest(expected, supplied):
            return True
        self._json(401, {"error": "unauthorised"})
        return False

    def _read_json(self):
        length = int(self.headers.get("Content-Length", "0"))
        if length <= 0 or length > MAX_REQUEST_BODY:
            raise ValueError("invalid request body size")
        try:
            payload = json.loads(self.rfile.read(length))
        except json.JSONDecodeError as error:
            raise ValueError("request body must be valid JSON") from error
        if not isinstance(payload, dict):
            raise ValueError("request body must be a JSON object")
        return payload

    def _json(self, status, payload):
        body = json.dumps(payload, separators=(",", ":")).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(body)

    def _events(self):
        subscriber = self.integration.events.subscribe()
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "keep-alive")
        self.end_headers()
        try:
            ready = {"type": "ready", "timestamp": time.time(), "data": self.integration.status()}
            self._write_event(ready)
            while True:
                try:
                    event = subscriber.get(timeout=15)
                    self._write_event(event)
                except queue.Empty:
                    self.wfile.write(b": keepalive\n\n")
                    self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            pass
        finally:
            self.integration.events.unsubscribe(subscriber)

    def _write_event(self, event):
        event_type = str(event["type"]).replace("\n", "")
        data = json.dumps(event, separators=(",", ":"))
        self.wfile.write(f"event: {event_type}\ndata: {data}\n\n".encode("utf-8"))
        self.wfile.flush()

    def log_message(self, format_string, *args):
        RNS.log(
            f"Bridge HTTP {self.command} {self.path} {format_string % args}",
            RNS.LOG_DEBUG,
        )


def main():
    integration = LXMFIntegration()
    BridgeHandler.integration = integration
    BridgeHandler.api_token = os.getenv("LXMF_BRIDGE_TOKEN", "").strip()
    address = os.getenv("LXMF_BRIDGE_LISTEN_ADDRESS", "0.0.0.0")
    port = int(os.getenv("LXMF_BRIDGE_PORT", "8081"))
    server = BridgeHTTPServer((address, port), BridgeHandler)

    def stop_server(_signum, _frame):
        threading.Thread(target=server.shutdown, daemon=True).start()

    signal.signal(signal.SIGINT, stop_server)
    signal.signal(signal.SIGTERM, stop_server)
    RNS.log(
        f"LXMF bridge ready on {integration.source.hash.hex()} at {address}:{port}",
        RNS.LOG_NOTICE,
    )
    server.serve_forever(poll_interval=0.5)
    server.server_close()


if __name__ == "__main__":
    main()
