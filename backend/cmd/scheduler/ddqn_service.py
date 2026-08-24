# Entry point redirect to dqn_service.py
from dqn_service import *

if __name__ == "__main__":
    import argparse
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
