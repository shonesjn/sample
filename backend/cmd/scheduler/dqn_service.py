import argparse
import json
import os
import random
import threading
import time
from collections import deque
from http.server import BaseHTTPRequestHandler, HTTPServer
from socketserver import ThreadingMixIn

import torch
import torch.nn as nn
import torch.optim as optim

torch.set_num_threads(1)


class QNetwork(nn.Module):
    """Deep Q-Network with 18-dimensional state input, LayerNorm, and 3 action outputs."""
    def __init__(self, state_dim=18, action_dim=3, hidden_dim=128, use_layer_norm=True):
        super().__init__()
        if use_layer_norm:
            self.net = nn.Sequential(
                nn.Linear(state_dim, hidden_dim),
                nn.LayerNorm(hidden_dim),
                nn.ReLU(),
                nn.Linear(hidden_dim, hidden_dim),
                nn.LayerNorm(hidden_dim),
                nn.ReLU(),
                nn.Linear(hidden_dim, action_dim)
            )
        else:
            self.net = nn.Sequential(
                nn.Linear(state_dim, hidden_dim),
                nn.ReLU(),
                nn.Linear(hidden_dim, hidden_dim),
                nn.ReLU(),
                nn.Linear(hidden_dim, action_dim)
            )

    def forward(self, x):
        return self.net(x)


class ReplayBuffer:
    """Thread-safe cyclic experience replay buffer for Double DQN."""
    def __init__(self, capacity=50000):
        self.buffer = deque(maxlen=capacity)
        self.lock = threading.Lock()

    def push(self, state, action, reward, next_state, done):
        with self.lock:
            self.buffer.append((state, action, reward, next_state, done))

    def sample(self, batch_size):
        with self.lock:
            batch = random.sample(self.buffer, min(len(self.buffer), batch_size))
            states, actions, rewards, next_states, dones = zip(*batch)
            return (
                torch.tensor(states, dtype=torch.float32),
                torch.tensor(actions, dtype=torch.long),
                torch.tensor(rewards, dtype=torch.float32),
                torch.tensor(next_states, dtype=torch.float32),
                torch.tensor(dones, dtype=torch.float32),
            )

    def __len__(self):
        with self.lock:
            return len(self.buffer)


class DDQNAgent:
    """
    Real Double Deep Q-Network (DDQN) Agent.
    Decouples action selection (Policy Network) from action evaluation (Target Network)
    to eliminate maximization bias present in standard DQN.
    """
    def __init__(
        self,
        state_dim=18,
        action_dim=3,
        hidden_dim=128,
        lr=1e-3,
        gamma=0.95,
        tau=0.005,
        epsilon=0.30,
        epsilon_decay=0.995,
        epsilon_min=0.05,
        buffer_size=50000,
        batch_size=32
    ):
        self.state_dim = state_dim
        self.action_dim = action_dim
        self.gamma = gamma
        self.tau = tau
        self.epsilon = epsilon
        self.epsilon_decay = epsilon_decay
        self.epsilon_min = epsilon_min
        self.batch_size = batch_size

        # Primary Policy Network and Target Network
        self.policy_net = QNetwork(state_dim, action_dim, hidden_dim)
        self.target_net = QNetwork(state_dim, action_dim, hidden_dim)
        self.target_net.load_state_dict(self.policy_net.state_dict())
        self.target_net.eval()

        self.optimizer = optim.Adam(self.policy_net.parameters(), lr=lr)
        self.criterion = nn.SmoothL1Loss()  # Huber loss for RL stability
        self.memory = ReplayBuffer(buffer_size)

        self.lock = threading.Lock()
        self.train_step_count = 0
        self.last_state = None
        self.last_action = None
        self.last_time = time.time()

        self._load_initial_weights()

    def _load_initial_weights(self):
        """Load pretrained weights from ddqn_model.pth or fallback to dqn_model.pth."""
        curr_dir = os.path.dirname(__file__)
        ddqn_path = os.path.join(curr_dir, "ddqn_model.pth")
        dqn_path = os.path.join(curr_dir, "dqn_model.pth")

        loaded = False
        for path in [ddqn_path, dqn_path]:
            if os.path.exists(path):
                try:
                    checkpoint = torch.load(path, map_location=torch.device('cpu'), weights_only=False)
                    if isinstance(checkpoint, dict) and "online_state_dict" in checkpoint:
                        self.policy_net.load_state_dict(checkpoint["online_state_dict"])
                        if "target_state_dict" in checkpoint:
                            self.target_net.load_state_dict(checkpoint["target_state_dict"])
                        else:
                            self.target_net.load_state_dict(checkpoint["online_state_dict"])
                    elif isinstance(checkpoint, dict) and "net.0.weight" in checkpoint:
                        self.policy_net.load_state_dict(checkpoint)
                        self.target_net.load_state_dict(checkpoint)
                    else:
                        self.policy_net.load_state_dict(checkpoint)
                        self.target_net.load_state_dict(checkpoint)
                    print(f"[DDQN] Successfully loaded weights from {os.path.basename(path)}")
                    loaded = True
                    break
                except Exception as e:
                    print(f"[DDQN] Warning: Could not load {path}: {e}")
        
        if not loaded:
            print("[DDQN] No weight checkpoint found; initialized policy & target networks with fresh weights.")

    def select_action(self, state, evaluate=False):
        """
        Inference step with accurate sub-millisecond timer profiling.
        Returns: (action, q_values, is_exploration, explore_time_ms, exploit_time_ms)
        """
        state_t = torch.tensor(state, dtype=torch.float32).unsqueeze(0)

        # 1. Measure exploration decision overhead
        t0 = time.perf_counter()
        is_exploration = (not evaluate) and (random.random() < self.epsilon)
        t1 = time.perf_counter()
        explore_time_ms = (t1 - t0) * 1000.0

        # 2. Neural Network Forward Pass (Exploitation)
        with torch.inference_mode():
            t2 = time.perf_counter()
            q_values_tensor = self.policy_net(state_t)
            t3 = time.perf_counter()
            exploit_time_ms = (t3 - t2) * 1000.0

        q_values = q_values_tensor.squeeze(0).tolist()

        if is_exploration:
            action = random.randint(0, self.action_dim - 1)
        else:
            action = int(torch.argmax(q_values_tensor, dim=1).item())

        # Epsilon decay
        if not evaluate:
            with self.lock:
                self.epsilon = max(self.epsilon * self.epsilon_decay, self.epsilon_min)

        return action, q_values, is_exploration, explore_time_ms, exploit_time_ms

    def compute_reward(self, prev_state, action, curr_state):
        """
        Calculates multi-objective edge QoS reward from continuous state transition:
        - High Throughput (+)
        - Low End-to-End Latency (-)
        - Low In-Flight Backlog & Queue (-)
        - Safe Thermal Profile (-)
        """
        # State layout per node (6 features x 3 nodes): [cpu, mem, temp, inflight, lat, throughput]
        # Aggregate across 3 nodes
        curr_lat = sum(curr_state[4 + i * 6] for i in range(3)) / 3.0
        curr_tp = sum(curr_state[5 + i * 6] for i in range(3))
        curr_inflight = sum(curr_state[3 + i * 6] for i in range(3))
        curr_temp = max(curr_state[2 + i * 6] for i in range(3))

        # Normalized components
        r_throughput = curr_tp / 30.0
        r_latency = - (curr_lat / 100.0)
        r_inflight = - (curr_inflight / 45.0)
        r_thermal = - 1.0 if curr_temp > 80.0 else 0.0

        # Composite reward
        reward = (0.40 * r_throughput) + (0.35 * r_latency) + (0.20 * r_inflight) + (0.05 * r_thermal)
        return float(reward)

    def record_transition(self, current_state, action):
        """Stores transition into experience replay and triggers background DDQN learning."""
        with self.lock:
            if self.last_state is not None and self.last_action is not None:
                reward = self.compute_reward(self.last_state, self.last_action, current_state)
                self.memory.push(self.last_state, self.last_action, reward, current_state, False)

            self.last_state = list(current_state)
            self.last_action = action

    def learn_step(self):
        """
        Core Double DQN Learning Step:
        1. Sample mini-batch (s, a, r, s', done)
        2. Evaluate optimal next action with Policy Network: a* = argmax_a Q_policy(s', a)
        3. Evaluate Q-value of a* using Target Network: Q_target(s', a*)
        4. Compute TD target: y = r + gamma * (1 - done) * Q_target(s', a*)
        5. Minimize Smooth L1 loss and soft-update Target Network weights.
        """
        if len(self.memory) < self.batch_size:
            return 0.0

        states, actions, rewards, next_states, dones = self.memory.sample(self.batch_size)

        # Current Q-values: Q_policy(s, a)
        current_q = self.policy_net(states).gather(1, actions.unsqueeze(1)).squeeze(1)

        with torch.no_grad():
            # DDQN Step: Action selection with Policy Net, Value evaluation with Target Net
            next_actions = self.policy_net(next_states).argmax(dim=1, keepdim=True)
            next_q_values = self.target_net(next_states).gather(1, next_actions).squeeze(1)
            target_q = rewards + (1.0 - dones) * self.gamma * next_q_values

        loss = self.criterion(current_q, target_q)

        self.optimizer.zero_grad()
        loss.backward()
        nn.utils.clip_grad_norm_(self.policy_net.parameters(), max_norm=5.0)
        self.optimizer.step()

        # Polyak Soft Update of Target Network: theta_target = tau * theta_policy + (1 - tau) * theta_target
        with torch.no_grad():
            for target_param, policy_param in zip(self.target_net.parameters(), self.policy_net.parameters()):
                target_param.data.copy_(self.tau * policy_param.data + (1.0 - self.tau) * target_param.data)

        self.train_step_count += 1
        return float(loss.item())

    def save_weights(self, path=None):
        """Persist current policy network weights."""
        if path is None:
            path = os.path.join(os.path.dirname(__file__), "ddqn_model.pth")
        try:
            torch.save(self.policy_net.state_dict(), path)
            print(f"[DDQN] Saved model weights to {path}")
            return True
        except Exception as e:
            print(f"[DDQN] Error saving weights: {e}")
            return False


# Global DDQN Agent instance
AGENT: DDQNAgent = DDQNAgent()


def background_training_worker(agent, interval_sec=0.1):
    """Runs DDQN mini-batch updates in background without interfering with low-latency HTTP requests."""
    while True:
        try:
            if agent and len(agent.memory) >= agent.batch_size:
                agent.learn_step()
            time.sleep(interval_sec)
        except Exception:
            time.sleep(1.0)


class DDQNHandler(BaseHTTPRequestHandler):
    """HTTP Request Handler for Double DQN scheduling inference and feedback."""

    def log_message(self, format, *args):
        """Suppress default HTTP logging for maximum zero-overhead throughput."""
        pass

    def _send_json(self, status_code, data):
        """Helper to send JSON response."""
        self.send_response(status_code)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Access-Control-Allow-Origin', '*')
        self.end_headers()
        self.wfile.write(json.dumps(data).encode('utf-8'))

    def do_OPTIONS(self):
        """Handle CORS preflight requests."""
        self.send_response(200)
        self.send_header('Access-Control-Allow-Origin', '*')
        self.send_header('Access-Control-Allow-Methods', 'POST, GET, OPTIONS')
        self.send_header('Access-Control-Allow-Headers', 'Content-Type')
        self.end_headers()

    def do_GET(self):
        """Health and algorithm status check."""
        if self.path == '/health':
            self._send_json(200, {
                "status": "ok",
                "algorithm": "Double-DQN (DDQN)",
                "model_loaded": AGENT is not None,
                "replay_buffer_size": len(AGENT.memory) if AGENT else 0,
                "train_steps": AGENT.train_step_count if AGENT else 0,
                "epsilon": AGENT.epsilon if AGENT else 0.0
            })
        else:
            self._send_json(404, {"error": "not found"})

    def do_POST(self):
        """Main prediction and feedback endpoints."""
        if self.path == '/predict':
            content_length = int(self.headers.get('Content-Length', 0))
            if content_length == 0:
                self._send_json(400, {"error": "empty body"})
                return

            post_data = self.rfile.read(content_length)
            try:
                data = json.loads(post_data.decode('utf-8'))
            except json.JSONDecodeError as e:
                self._send_json(400, {"error": f"invalid json: {str(e)}"})
                return

            state = data.get('state')
            if state is None or not isinstance(state, list) or len(state) != 18:
                self._send_json(400, {
                    "error": "state must be a list of 18 floats",
                    "received": state
                })
                return

            try:
                # Real DDQN Action Selection & Exploitation Inference
                action, q_values, is_exploration, explore_time_ms, exploit_time_ms = AGENT.select_action(
                    state, evaluate=False
                )

                # Record transition for continuous online Double-DQN experience replay learning
                AGENT.record_transition(state, action)

                response = {
                    "action": action,
                    "q_values": q_values,
                    "exploration_time_ms": explore_time_ms,
                    "exploitation_time_ms": exploit_time_ms,
                    "is_exploration": is_exploration,
                    "epsilon": AGENT.epsilon,
                    "algorithm": "DDQN"
                }
                self._send_json(200, response)
            except Exception as e:
                self._send_json(500, {"error": str(e)})

        elif self.path == '/save':
            success = AGENT.save_weights()
            self._send_json(200, {"status": "saved" if success else "error"})

        elif self.path == '/train':
            loss = AGENT.learn_step()
            self._send_json(200, {"status": "trained", "loss": loss, "train_steps": AGENT.train_step_count})

        else:
            self._send_json(404, {"error": "not found"})


class ThreadedHTTPServer(ThreadingMixIn, HTTPServer):
    """Thread-per-request HTTP server."""
    allow_reuse_address = True
    daemon_threads = True


def run_server(host='0.0.0.0', port=5010):
    """Launch the Double DQN HTTP sidecar service."""
    global AGENT
    if AGENT is None:
        AGENT = DDQNAgent()

    server = ThreadedHTTPServer((host, port), DDQNHandler)
    print(f"[DDQN] Double Deep Q-Network Server running on http://{host}:{port}")
    print(f"[DDQN] Neural Architecture: PolicyNet + TargetNet (18 inputs -> 128 -> 128 -> 3 actions)")
    print(f"[DDQN] Endpoints: GET /health, POST /predict, POST /train, POST /save")
    print(f"[DDQN] Initial epsilon={AGENT.epsilon:.3f}, decay={AGENT.epsilon_decay}, min={AGENT.epsilon_min}")

    # Start background experience replay training worker
    trainer_thread = threading.Thread(target=background_training_worker, args=(AGENT,), daemon=True)
    trainer_thread.start()

    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\n[DDQN] Shutdown signal received. Saving model...")
        AGENT.save_weights()
    finally:
        server.server_close()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Double DQN (DDQN) RL Sidecar Service for Edge Scheduler")
    parser.add_argument("--host", type=str, default="0.0.0.0", help="Bind address")
    parser.add_argument("--port", type=int, default=5010, help="Listen port")
    parser.add_argument("--epsilon", type=float, default=0.30, help="Initial exploration rate")
    parser.add_argument("--epsilon-decay", type=float, default=0.995, help="Epsilon decay rate")
    parser.add_argument("--epsilon-min", type=float, default=0.05, help="Minimum epsilon")
    parser.add_argument("--lr", type=float, default=1e-3, help="Learning rate")
    parser.add_argument("--gamma", type=float, default=0.95, help="Discount factor")
    parser.add_argument("--tau", type=float, default=0.005, help="Target network soft update rate")
    parser.add_argument("--buffer-size", type=int, default=50000, help="Replay buffer capacity")
    parser.add_argument("--batch-size", type=int, default=32, help="Mini-batch training size")
    args = parser.parse_args()

    # Initialize DDQN Agent
    AGENT = DDQNAgent(
        state_dim=18,
        action_dim=3,
        hidden_dim=128,
        lr=args.lr,
        gamma=args.gamma,
        tau=args.tau,
        epsilon=args.epsilon,
        epsilon_decay=args.epsilon_decay,
        epsilon_min=args.epsilon_min,
        buffer_size=args.buffer_size,
        batch_size=args.batch_size
    )

    run_server(host=args.host, port=args.port)