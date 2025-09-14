# Maestro

The Maestro is a gRPC microservice that acts as the orchestrator of the pizza ordering system. This service coordinates the entire pizza preparation workflow, managing orders from start to finish while maintaining realistic human behavior patterns including lunch breaks and smoking sessions.

## Service Overview

The Maestro service provides the following functionality:

- **Order Processing**: Consumes pizza orders from NATS JetStream queues and coordinates their preparation
- **Workflow Orchestration**: Manages the flow between different services (panettiere, fornaio, delivery)
- **Batch Processing**: Handles orders in configurable batches for efficient processing
- **Human Behavior Simulation**: Implements realistic work patterns including lunch breaks and smoking sessions
- **Message Queue Integration**: Uses NATS JetStream for reliable order processing and delivery coordination
- **Health Monitoring**: Provides health checks based on NATS connectivity status

## Service Behavior

### Order Processing Workflow
1. **Order Consumption**: Fetches pending orders from NATS JetStream in configurable batches
2. **Dough Request**: Coordinates with the panettiere service to prepare pizza dough
3. **Order Advancement**: Moves processed orders to the delivery queue for the next stage
4. **Smoking Break**: Takes a configurable smoking break after each order (with potential oversmoking)

### Human Behavior Patterns
The maestro follows realistic work patterns:

- **Lunch Breaks**: Takes regular lunch breaks at configurable intervals
- **Smoking Sessions**: Has a smoking break after processing each order
- **Oversmoking**: Random chance to smoke longer than planned (configurable probability)
- **Batch Work**: Processes orders in batches rather than one-by-one for efficiency

### Message Queue Integration
- **Input Queue**: `orders.waiting_to_cook.*` - Orders ready for processing
- **Output Queue**: `orders.waiting_delivery.*` - Orders ready for delivery
- **Stream**: Uses NATS JetStream for reliable message processing with acknowledgments

## API Endpoints

### SayHello
Simple greeting endpoint for service testing:
- **Input**: Name string
- **Output**: Greeting message

## Service Architecture

```mermaid
graph TB
    subgraph "Maestro Service"
        M[Maestro Handler]
        LC[Lunch Controller]
        SC[Smoking Controller]
        BP[Batch Processor]
    end
    
    subgraph "External Services"
        P[Panettiere Service]
        N[NATS JetStream]
        H[Health Check]
    end
    
    subgraph "Message Flow"
        IQ[orders.waiting_to_cook.*]
        OQ[orders.waiting_delivery.*]
    end
    
    N --> IQ
    IQ --> BP
    BP --> M
    M --> P
    P --> M
    M --> OQ
    OQ --> N
    
    LC -.-> M
    M -.-> SC
    
    N --> H
    H --> M
    
    style M fill:#ff9999
    style P fill:#99ccff
    style N fill:#99ff99
```

## Order Processing Flow

```mermaid
sequenceDiagram
    participant JS as NATS JetStream
    participant M as Maestro
    participant P as Panettiere
    participant Timer as Lunch Timer
    
    Note over M,Timer: Main Processing Loop
    
    loop Every Processing Cycle
        M->>JS: Fetch batch of orders
        JS-->>M: Return orders (up to batch size)
        
        loop For each order
            M->>M: Set order in progress
            M->>P: Request dough (MakeDough)
            
            alt Panettiere available
                P-->>M: Dough ready
                M->>M: Process order
                M->>JS: Send to delivery queue
                M->>M: Acknowledge order
                M->>M: Smoke break (with potential oversmoking)
            else Panettiere sleeping
                P-->>M: ResourceExhausted error
                Note over M: Order remains unprocessed
            end
        end
        
        alt Lunch time reached
            Timer->>M: Lunch time signal
            M->>M: Take lunch break
            Note over M: Configurable lunch duration
        end
    end
```

## Work Schedule Diagram

```mermaid
stateDiagram-v2
    [*] --> Idle
    
    Idle --> ProcessingBatch : New orders available
    Idle --> Lunching : Lunch timer
    
    ProcessingBatch --> ProcessingOrder : For each order in batch
    ProcessingOrder --> RequestingDough : Order started
    RequestingDough --> Smoking : Dough received
    RequestingDough --> ProcessingOrder : Panettiere unavailable
    
    Smoking --> ProcessingOrder : More orders in batch
    Smoking --> Idle : Batch complete
    
    Lunching --> Idle : Lunch finished
    
    ProcessingOrder : Coordinate with panettiere
    ProcessingOrder : Send to delivery queue
    
    Smoking : Post-order smoking break
    Smoking : Potential oversmoking
    
    Lunching : Regular lunch break
    Lunching : Fixed duration
    
    note right of Smoking
        Smoking after each order:
        - Base smoking time
        - Potential oversmoking
        - Configurable probability
    end note
    
    note right of Lunching
        Regular lunch breaks:
        - Configurable interval
        - Fixed duration
        - Blocks order processing
    end note
```

## Configuration

The service behavior is controlled through various settings:

### Order Processing
- `OrderBatchSize`: Number of orders to fetch in each batch
- `FetchMaxWaitInSeconds`: Maximum time to wait when fetching orders

### Human Behavior
- `SmokingDurationInSeconds`: Base time for smoking breaks
- `ProbabilityOfOversmoking`: Chance of smoking longer than planned (0.0-1.0)
- `OversmokingFactor`: Multiplier for oversmoking duration
- `PeriodBetweenLunchInSeconds`: Interval between lunch breaks
- `LunchDurationInSeconds`: Duration of lunch breaks

### External Dependencies
- `PanettiereClient`: gRPC client configuration for panettiere service
- `Nats`: NATS connection and JetStream configuration

## Health Checks

The service provides health status based on external dependencies:
- **SERVING**: When NATS connection is healthy
- **NOT_SERVING**: When NATS connection is down

Health checks are updated asynchronously and reflect the service's ability to process orders.

## Metrics and Observability

The service provides OpenTelemetry metrics for monitoring:

### Counters
- `maestro.lunch.count`: Number of lunch breaks taken
- `maestro.smoke.count`: Number of smoking sessions

### Histograms
- `maestro.lunch.duration`: Duration of lunch breaks
- `maestro.smoke.duration`: Duration of smoking sessions

### Tracing
- Full distributed tracing for order processing workflow
- Span correlation across service boundaries
- Error tracking and performance monitoring
