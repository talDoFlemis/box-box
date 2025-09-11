# Panettiere

The Panettiere is a gRPC microservice that simulates a baker who makes pizza dough on demand. This service is part of a larger pizza ordering system and demonstrates realistic behavior patterns including work cycles, sleep schedules, and performance variance.

## Service Overview

The Panettiere service provides the following functionality:

- **Dough Making**: Creates pizza dough based on order specifications (size and border type)
- **Realistic Timing**: Simulates actual dough preparation time with configurable variance
- **Sleep Cycles**: Implements periodic sleep patterns that affect service availability
- **Health Monitoring**: Provides health checks that reflect the baker's current state
- **Order Tracking**: Associates each dough creation with specific order IDs for traceability

## Service Behavior

### Work Patterns
- The panettiere works on one dough order at a time
- Dough preparation time varies based on configured factors to simulate real-world conditions
- Each dough is customized according to border type (e.g., thin, thick) and pizza size

### Sleep Schedule
The panettiere follows a realistic sleep pattern:
- **Periodic Sleep**: Goes to sleep at regular intervals when not actively working
- **Sleep Duration**: Configurable base sleep time with potential for oversleeping
- **Oversleeping**: Random chance to sleep longer than planned (configurable probability)
- **Work Priority**: Won't go to sleep while actively making dough
- **Deferred Sleep**: If sleep time arrives during work, will sleep after completing current order

### Service States
- **Idle**: Ready to accept new dough orders
- **Working**: Currently making dough for an order
- **Sleeping**: Unavailable, will reject new orders with ResourceExhausted error
- **Should Sleep**: Marked for sleep after current work completion

## API Endpoints

### MakeDough
Creates pizza dough according to specifications:
- **Input**: Order ID, border type, pizza size
- **Output**: Dough description
- **Behavior**: Returns error if panettiere is sleeping

### Status
Returns current panettiere status:
- **Output**: Current activity state (idle, working, sleeping, etc.)

## Flow Diagram

```mermaid
stateDiagram-v2
    [*] --> Idle
    
    Idle --> Working : MakeDough Request
    Idle --> Sleeping : Sleep Timer
    
    Working --> Idle : Dough Complete & No Pending Sleep
    Working --> ShouldSleep : Sleep Timer During Work
    ShouldSleep --> Sleeping : Work Complete
    
    Sleeping --> Idle : Wake Up Timer
    
    Working : Making dough for order
    Working : Rejects new requests
    
    Sleeping : Unavailable
    Sleeping : Returns ResourceExhausted
    
    ShouldSleep : Finishing current work
    ShouldSleep : Will sleep after completion
    
    note right of Sleeping
        Sleep duration can vary:
        - Base sleep time
        - Potential oversleeping
        - Configurable probability
    end note
    
    note right of Working
        Dough making time varies:
        - Base preparation time
        - Random variance factor
        - Different for size/border
    end note
```

## Sleep Cycle Details

```mermaid
sequenceDiagram
    participant Timer as Sleep Timer
    participant P as Panettiere
    participant Client as Client Request
    
    Note over Timer,Client: Normal Sleep Cycle
    Timer->>P: Sleep time reached
    P->>P: Check if working
    alt Not working
        P->>P: Go to sleep immediately
        P-->>Client: Reject requests (ResourceExhausted)
        Note over P: Sleep duration + potential oversleep
        P->>P: Wake up
        P->>P: Status: Idle
    else Currently working
        P->>P: Mark "should sleep"
        Note over P: Continue current work
        P->>P: Complete current dough
        P->>P: Go to sleep
        Note over P: Sleep duration + potential oversleep
        P->>P: Wake up
        P->>P: Status: Idle
    end
```

## Configuration

The service behavior is controlled through various settings:

- `TimeToMakeADoughInSeconds`: Base time for dough preparation
- `VarianceInDoughMakeInSecondsFactor`: Multiplier for time variance
- `PeriodBetweenSleepInSeconds`: Interval between sleep cycles
- `SleepDurationInSeconds`: Base sleep duration
- `ProbabilityOfOversleeping`: Chance of sleeping longer than planned
- `OversleepingFactor`: Multiplier for oversleep duration

## Health Checks

The service provides health status that reflects the panettiere's availability:
- **SERVING**: When idle or working (can accept new orders)
- **NOT_SERVING**: When sleeping (cannot accept new orders)

Health checks are updated asynchronously based on the current state.